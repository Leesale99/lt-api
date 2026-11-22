#!/usr/bin/env bash
# Build the `pi-worker:v1` sbx template from the v3 kit in ./kit.
#
# Run from anywhere (paths are resolved from this script):
#   .pi/sandboxes/build.sh
#
# The kit is a v3 workload: `kit/pi-worker.yaml` (descriptor) + the stem-paired
# `kit/pi-worker.dockerfile` (recipe). Go, pi and the agent config are IMAGE
# content, so there is no install phase and no pack step: the kit directory is
# the artifact — `sbx create <kit-dir> [PATH...]` builds and boots it in one go.
#
# Optional env:
#   KEEP=1               keep the build/sanity sandboxes around for inspection.
#   MODEL_CALL=0         skip the in-VM live model call.
#   SKIP_REBUILD=1       reuse an existing pi-worker:v1 template (no rebuild).
#
# Everything the run proves is logged under .pi/sandboxes/dist/logs/.
#
# Long steps (sandbox create, GOMODCACHE warm, live model call, template save)
# run DETACHED: their output goes to their own step log under dist/logs/ and
# the script polls it, printing a heartbeat every POLL_SECS. A step therefore
# never blocks silently — an agent run that watches this log sees progress, and
# a real hang is bounded by the per-step max instead of an unbounded silence.
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SANDBOXES_DIR="$REPO_ROOT/.pi/sandboxes"
KIT_DIR="$SANDBOXES_DIR/kit"
DIST_DIR="$SANDBOXES_DIR/dist"
LOG_DIR="$DIST_DIR/logs"
WORKTREE="$REPO_ROOT"

BUILD_SANDBOX="pi-worker-build"
SANITY_SANDBOX="pi-worker-sanity"
TEMPLATE_TAG="pi-worker:v1"
CPUS=4
MEMORY=8g
KIT_YAML="$KIT_DIR/pi-worker.yaml"
KIT_RECIPE="$KIT_DIR/pi-worker.dockerfile"
MODEL_CALL="${MODEL_CALL:-1}"
SKIP_REBUILD="${SKIP_REBUILD:-0}"
KEEP="${KEEP:-0}"

# Pins echoed in the run log and checked in-VM.
# Go comes from the repo's go.mod (`go` directive) and must match the recipe's
# ARG; pi is pinned in the recipe and cross-checked against the host install.
GO_PIN=$(awk '$1 == "go" { print $2; exit }' "$REPO_ROOT/go.mod")
PI_PIN=$(pi --version 2>/dev/null | tail -n 1 | tr -d '[:space:]')
KIT_GO_PIN=$(sed -n 's/^ARG GO_VERSION=//p' "$KIT_RECIPE" 2>/dev/null | head -n 1)
KIT_PI_PIN=$(sed -n 's/^ARG PI_VERSION=//p' "$KIT_RECIPE" 2>/dev/null | head -n 1)
BASE_IMAGE="docker/sandbox-templates:shell-docker"

RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
LOG="$LOG_DIR/build-$RUN_ID.log"
TIMINGS_FILE="$DIST_DIR/timings.tsv"
TIMINGS_MD="$DIST_DIR/timings-$RUN_ID.md"

mkdir -p "$LOG_DIR"
: >"$LOG"

TIMING_ROWS=()

now_ms() { python3 -c 'import time; print(int(time.time() * 1000))'; }

say() { printf '%s\n' "$*" | tee -a "$LOG"; }

slugify() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-' | cut -c1-48; }

# run_timed <label> <cmd...>
# Streams combined output to stdout + log, records wall-clock ms.
run_timed() {
    local label="$1"; shift
    local start end rc=0
    start=$(now_ms)
    say ""
    say "===== $label ====="
    say "\$ $*"
    if [ "${TIMEOUT_SECS:-0}" -gt 0 ]; then
        with_timeout "$TIMEOUT_SECS" "$@" 2>&1 | tee -a "$LOG"
        rc=${PIPESTATUS[0]}
    else
        "$@" 2>&1 | tee -a "$LOG"
        rc=${PIPESTATUS[0]}
    fi
    end=$(now_ms)
    local ms=$((end - start))
    TIMING_ROWS+=("$label	$ms	$([ $rc -eq 0 ] && echo ok || echo "exit $rc")")
    say "[timing] $label: ${ms} ms (exit $rc)"
    return $rc
}

# run_detached <label> <max_secs> <poll_secs> <cmd...>
# Runs CMD in the background with its output in a per-step log, then polls that
# log every <poll_secs> and echoes a heartbeat, so neither this script nor a
# supervising agent is ever silent for long. Output is folded back into the main
# log when the step ends. Returns the command's exit status.
run_detached() {
    local label="$1" max_secs="$2" poll_secs="$3"; shift 3
    local step_log="$LOG_DIR/step-$(slugify "$label")-$RUN_ID.log"
    local marker="STEP_EXIT"
    local start end ms pid waited=0 cmd_rc

    start=$(now_ms)
    say ""
    say "===== $label (detached) ====="
    say "\$ $*"
    say "  step log: $step_log"
    say "  budget: ${max_secs}s, polling every ${poll_secs}s"

    (
        "$@" 2>&1
        printf '\n%s:%s\n' "$marker" "$?"
    ) >"$step_log" 2>&1 &
    pid=$!

    while kill -0 "$pid" 2>/dev/null; do
        sleep "$poll_secs"
        waited=$((waited + poll_secs))
        say "  [wait] ${label}: ${waited}s elapsed, $(wc -c <"$step_log" | tr -d ' ') bytes in step log"
        if [ "$waited" -ge "$max_secs" ]; then
            say "  [wait] BUDGET EXHAUSTED (${max_secs}s) — terminating $label"
            kill -TERM "$pid" 2>/dev/null || true
            sleep 2
            kill -KILL "$pid" 2>/dev/null || true
            break
        fi
    done
    wait "$pid" 2>/dev/null
    end=$(now_ms)
    ms=$((end - start))

    cmd_rc=$(sed -n "s/^${marker}://p" "$step_log" 2>/dev/null | tail -n 1)
    [ -z "$cmd_rc" ] && cmd_rc=124
    sed "/^${marker}:/d" "$step_log" 2>/dev/null >>"$LOG"

    TIMING_ROWS+=("$label	$ms	$([ "$cmd_rc" -eq 0 ] && echo ok || echo "exit $cmd_rc")")
    say "[timing] $label: ${ms} ms (exit $cmd_rc)"
    return "$cmd_rc"
}

# Portable timeout wrapper (macOS has no `timeout`).
with_timeout() {
    local secs=$1; shift
    if command -v gtimeout >/dev/null 2>&1; then
        gtimeout "$secs" "$@"
        return $?
    fi
    "$@" &
    local pid=$!
    # The killer MUST NOT inherit our stdout: it would hold the pipe to `tee`
    # open for the whole sleep, so a 1 s command looks like it took <secs> and
    # its output only surfaces when the timer expires.
    ( sleep "$secs"; kill -TERM "$pid" 2>/dev/null ) >/dev/null 2>&1 &
    local watcher=$!
    wait "$pid"
    local rc=$?
    kill "$watcher" 2>/dev/null || true
    wait "$watcher" 2>/dev/null || true
    return $rc
}

cleanup_sandboxes() {
    if [ "$KEEP" = "1" ]; then
        say ""
        say "KEEP=1: leaving $BUILD_SANDBOX / $SANITY_SANDBOX in place."
        return
    fi
    for name in "$SANITY_SANDBOX" "$BUILD_SANDBOX"; do
        if sandbox_exists "$name"; then
            say "cleanup: removing sandbox $name"
            sbx rm "$name" --force >/dev/null 2>&1 || true
        fi
    done
}

trap 'cleanup_sandboxes' EXIT

sandbox_exists() { sbx ls -q 2>/dev/null | grep -qx "$1"; }
# `sbx template inspect` is --cloud-only in v1 ("requires --cloud"), so ask the
# local image store instead and match REPOSITORY:TAG by its last path segment
# ("pi-worker" == "docker.io/library/pi-worker").
template_exists() {
    local ref="$1" repo tag
    case "$ref" in
        *:*) repo="${ref%:*}"; tag="${ref##*:}" ;;
        *)   repo="$ref";       tag="latest" ;;
    esac
    sbx template ls 2>/dev/null | awk -v want="${repo##*/}" -v tag="$tag" '
        NR > 1 {
            n = split($1, parts, "/")
            if (parts[n] == want && $2 == tag) found = 1
        }
        END { exit(found ? 0 : 1) }'
}

say "pi-worker kit build — run $RUN_ID"
say "  repo worktree : $WORKTREE"
say "  kit descriptor: $KIT_YAML (schemaVersion 3, kind: workload)"
say "  kit recipe    : $KIT_RECIPE"
say "  base image    : $BASE_IMAGE (recipe FROM)"
say "  go pin        : $GO_PIN (go.mod) / ${KIT_GO_PIN:-<unset>} (recipe ARG)"
say "  pi pin        : ${KIT_PI_PIN:-<unset>} (recipe ARG) / $PI_PIN (host \`pi --version\`)"
say "  template tag  : $TEMPLATE_TAG"
say "  layout        : --cpus $CPUS --memory $MEMORY"

if [ -z "$GO_PIN" ] || [ -z "$PI_PIN" ] || [ -z "$KIT_GO_PIN" ] || [ -z "$KIT_PI_PIN" ]; then
    say "FATAL: could not resolve pins (go.mod go directive, host pi version, recipe ARG lines)"
    exit 1
fi
# The Go pin is hardcoded twice on purpose (repo toolchain + recipe), so a bump
# that lands in only one place is a hard failure here rather than a template
# built with a toolchain the repo does not pin.
if [ "$GO_PIN" != "$KIT_GO_PIN" ]; then
    say "FATAL: go.mod pins Go $GO_PIN but $KIT_RECIPE installs Go $KIT_GO_PIN — bump both"
    exit 1
fi
if [ "$PI_PIN" != "$KIT_PI_PIN" ]; then
    say "WARN: host pi is $PI_PIN but the template will ship $KIT_PI_PIN (recipe pin)."
    say "      Not fatal — the host version is only a cross-check — but host/VM drift"
    say "      is now visible in this log."
fi

# ---------------------------------------------------------------- 0. clean slate
if [ "$SKIP_REBUILD" = "1" ] && template_exists "$TEMPLATE_TAG"; then
    say "SKIP_REBUILD=1 and $TEMPLATE_TAG already exists — nothing to do."
    exit 0
fi

for name in "$SANITY_SANDBOX" "$BUILD_SANDBOX"; do
    if sandbox_exists "$name"; then
        say "pre-clean: removing stale sandbox $name"
        sbx rm "$name" --force >/dev/null 2>&1 || true
    fi
done
if template_exists "$TEMPLATE_TAG"; then
    say "pre-clean: $TEMPLATE_TAG exists and will be replaced at snapshot time"
    say "           (kept until then, so a failed run leaves a usable template)"
fi

# ------------------------------------------------- 1. validate the v3 kit
# There is no v1/v2 style `sbx kit validate` + `sbx kit pack` flow for a v3
# SOURCE kit: v3 validation IS the image build. The descriptor is echoed with
# `sbx kit inspect` (advisory — if this CLI rejects a v3 source directory, the
# build below is the authority and nothing is lost), then the recipe is built
# once with the sandbox-kit:3 frontend and thrown away (`--output
# type=cacheonly`), so a malformed descriptor or a broken RUN fails here, fast
# and cheap, instead of half way through `sbx create`.
mkdir -p "$DIST_DIR"

if run_timed "kit inspect (v3 descriptor echo)" sbx kit inspect "$KIT_DIR" --offline; then
    say "  descriptor accepted by this CLI"
else
    say "  WARN: \`sbx kit inspect\` did not accept the v3 source kit — the build"
    say "        step below is the validation authority, so this is not fatal."
fi

if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then
    run_detached "kit build (docker buildx, cacheonly) — v3 validation" 1800 30 \
        docker buildx build -f "$KIT_YAML" --output type=cacheonly "$KIT_DIR" || {
        say "FATAL: the kit recipe failed to build (descriptor or Dockerfile error)."
        say "       Nothing was snapshotted; fix $KIT_YAML / $KIT_RECIPE and re-run."
        exit 1
    }
else
    say "WARN: docker buildx not available — skipping the standalone kit build."
    say "      \`sbx create\` below still builds the image and fails on a bad kit,"
    say "      it just costs a sandbox-create attempt before the error surfaces."
fi

# ------------------------------------------------ 2. build sandbox from the kit
# The kit DIRECTORY is the first positional: it replaces the `shell` agent token
# of the template create form (`sbx create AGENT|SANDBOX_KIT [PATH...]`), and the
# workspaces follow it. There is no `-t` here — the base image is the recipe's
# FROM, and passing both a template and a kit would ask for two images.
# No install hook runs at create time: Go, pi and the agent config are already in
# the image, so this step only has to build and boot it.
run_detached "create build sandbox (v3 kit, image build)" 1800 20 \
    sbx create --name "$BUILD_SANDBOX" --pull never \
    --cpus "$CPUS" --memory "$MEMORY" "$KIT_DIR" "$WORKTREE" || {
    say "FATAL: could not create the build sandbox from $KIT_DIR."
    say "       If the error is about the template/pull policy, the base image"
    say "       ($BASE_IMAGE) is not local and --pull never blocked it; drop"
    say "       --pull never or pre-pull the base image."
    say "       If the error is about the kit positional, add"
    say "       -t $BASE_IMAGE to this create."
    exit 1
}

# -------------------------------------------------------------- 3. in-VM checks
TIMEOUT_SECS=180 run_timed "exec: go version" \
    sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" go version

TIMEOUT_SECS=120 run_timed "exec: pi --version" \
    sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" pi --version

TIMEOUT_SECS=120 run_timed "exec: worktree mounted at same absolute path" \
    sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c "test -f '$WORKTREE/go.mod' && pwd && ls -d '$WORKTREE'"

TIMEOUT_SECS=120 run_timed "exec: OPENCODE_GO_API_KEY placeholder prefix" \
    bash -c "sbx exec --no-input '$BUILD_SANDBOX' printenv OPENCODE_GO_API_KEY | head -c 8 | grep -q '^sbx-cs-' && echo 'placeholder prefix ok: sbx-cs-'"

# ------------------------------------------------- 4. warm the Go module cache
say ""
say "Warming GOMODCACHE in-VM (home of the agent user; captured by template save)."

# The worktree is mounted read-write in the sandbox, and `go mod download` writes
# go.sum: it records `h1:` hashes for the whole module graph, which `go mod tidy`
# then prunes — i.e. warming the cache would dirty the author's checkout and fail
# `go mod tidy -diff` in the merge gate. Snapshot the module files and put them
# back afterwards, whatever the warm did.
MOD_SNAPSHOT=$(mktemp -d)
for f in go.mod go.sum; do
    [ -f "$WORKTREE/$f" ] && cp "$WORKTREE/$f" "$MOD_SNAPSHOT/$f"
done
GOSUM_BEFORE=$(shasum -a 256 "$WORKTREE/go.sum" 2>/dev/null | cut -d' ' -f1)
say "  go.sum sha256 before warm: ${GOSUM_BEFORE:-<absent>}"

restore_module_files() {
    local f restored=0
    for f in go.mod go.sum; do
        [ -f "$MOD_SNAPSHOT/$f" ] || continue
        if ! cmp -s "$MOD_SNAPSHOT/$f" "$WORKTREE/$f"; then
            cp "$MOD_SNAPSHOT/$f" "$WORKTREE/$f"
            restored=1
            say "  restored worktree $f (the warm step rewrote it)"
        fi
    done
    rm -rf "$MOD_SNAPSHOT"
    if [ "$restored" = "1" ]; then
        say "  note: the module cache is still warm — only the go.sum bookkeeping"
        say "        the warm step added was undone."
    fi
    GOSUM_AFTER=$(shasum -a 256 "$WORKTREE/go.sum" 2>/dev/null | cut -d' ' -f1)
    say "  go.sum sha256 after warm:  ${GOSUM_AFTER:-<absent>}"
    if [ "${GOSUM_BEFORE:-x}" != "${GOSUM_AFTER:-y}" ]; then
        say "WARN: worktree go.sum changed across the warm step — check"
        say "      \`go mod tidy -diff\` before committing."
    fi
}

# `go mod download all` is the one genuinely long, genuinely quiet step (it
# prints nothing while it fetches). Run it detached and poll: a silent 10-minute
# pipe is what got an earlier run killed by a stall watchdog.
run_detached "exec: go mod download all (warm GOMODCACHE)" 2400 30 \
    sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" go mod download all || {
    say "WARN: go mod download failed for some modules — continuing, but the"
    say "      template's module cache may be incomplete."
}
restore_module_files

MODCACHE_PATHS=$(with_timeout 180 sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c \
    'echo "$(go env GOMODCACHE) $(go env GOPATH)"; du -sh "$(go env GOMODCACHE)" 2>/dev/null' 2>&1)
say "$MODCACHE_PATHS" | tee -a "$LOG"

TIMEOUT_SECS=120 run_timed "exec: GOPROXY/GOTOOLCHAIN (image env)" \
    sbx exec --no-input "$BUILD_SANDBOX" go env GOPROXY GOTOOLCHAIN GOSUMDB

# --------------------------------------------------- 5. live model call (cheap)
# Not fatal: this is a smoke signal that the host proxy + sentinel key + baked
# models.json all line up. The e2e pass re-proves the same path.
MODEL_CALL_STATUS="skipped"
if [ "$MODEL_CALL" = "1" ]; then
    say ""
    say "Live model call through the host proxy (placeholder key, no secret in VM)."
    if run_detached "exec: pi -p --model opencode-go/glm-5.3 'reply OK'" 300 15 \
        sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" \
        pi -p --no-session --model opencode-go/glm-5.3 "reply OK"; then
        MODEL_CALL_STATUS="ok"
    else
        MODEL_CALL_STATUS="failed (see log; not fatal — the e2e pass re-proves the path)"
        say "WARN: live model call failed — $MODEL_CALL_STATUS"
    fi
fi

# --------------------------------------------------------- 6. snapshot template
# Both are slow-ish and quiet (the snapshot prints nothing while it streams the
# filesystem), so poll them like the module warm.
run_detached "stop build sandbox" 180 10 sbx stop "$BUILD_SANDBOX" || say "WARN: stop returned non-zero"
# Drop the previous tag only now: everything expensive has already succeeded, so
# a failure before this point leaves the old template usable.
if template_exists "$TEMPLATE_TAG"; then
    say "replacing stale template $TEMPLATE_TAG"
    sbx template rm "$TEMPLATE_TAG" --force >/dev/null 2>&1 || true
fi
run_detached "template save $TEMPLATE_TAG" 1800 20 \
    sbx template save "$BUILD_SANDBOX" "$TEMPLATE_TAG" || {
    say "FATAL: template save failed"
    exit 1
}

# --------------------------------------- 7. fresh create from the saved template
run_detached "create from template (cold boot)" 600 15 \
    sbx create --name "$SANITY_SANDBOX" -t "$TEMPLATE_TAG" --pull never \
    --cpus "$CPUS" --memory "$MEMORY" shell "$WORKTREE" || {
    say "FATAL: could not create a sandbox from $TEMPLATE_TAG"
    exit 1
}

TIMEOUT_SECS=120 run_timed "sanity exec: go version" \
    sbx exec --no-input -w "$WORKTREE" "$SANITY_SANDBOX" go version
TIMEOUT_SECS=120 run_timed "sanity exec: pi --version" \
    sbx exec --no-input -w "$WORKTREE" "$SANITY_SANDBOX" pi --version
TIMEOUT_SECS=120 run_timed "sanity exec: placeholder prefix" \
    bash -c "sbx exec --no-input '$SANITY_SANDBOX' printenv OPENCODE_GO_API_KEY | head -c 8 | grep -q '^sbx-cs-' && echo 'placeholder prefix ok: sbx-cs-'"

# PI_AGENT_HOME is now image ENV (it was per-sandbox kit env in v2 and vanished
# with `sbx template save`), so the strict value has to be present here. This
# check fails loudly if a future edit moves it back into something per-sandbox.
TIMEOUT_SECS=120 run_timed "sanity exec: pi config + PI_AGENT_HOME baked into the image" \
    sbx exec --no-input -w "$WORKTREE" "$SANITY_SANDBOX" /bin/sh -c \
    'test -n "$PI_AGENT_HOME" || { echo "PI_AGENT_HOME is unset in a template sandbox" >&2; exit 1; }; echo "PI_AGENT_HOME=$PI_AGENT_HOME"; ls -l "$PI_AGENT_HOME/settings.json" "$PI_AGENT_HOME/models.json"; cat "$PI_AGENT_HOME/settings.json"; grep -c "\"id\":" "$PI_AGENT_HOME/models.json"'

# The warm GOMODCACHE is the entire point of the snapshot: an empty cache would
# silently push module fetches back onto the network in every task sandbox.
CACHE_REPORT=$(with_timeout 180 sbx exec --no-input "$SANITY_SANDBOX" /bin/sh -c \
    'du -sh "$(go env GOMODCACHE)"; find "$(go env GOMODCACHE)/cache/download" -name "*.zip" 2>/dev/null | wc -l' 2>&1)
say ""
say "GOMODCACHE in the fresh template sandbox (size, cached zips):"
say "$CACHE_REPORT"
if printf '%s\n' "$CACHE_REPORT" | head -n 1 | grep -q '^0'; then
    say "FATAL: GOMODCACHE came back empty — the snapshot did not capture"
    say "       /home/agent/go/pkg/mod, so the template is not worth shipping."
    exit 1
fi

# The v2 gap this build used to record — "kit env is per-sandbox, so a sandbox
# created from the template alone reports Go's defaults" — is closed: the strict
# values are image ENV (plus the agent's own go env file), so they must survive
# the snapshot. Assert that instead of just printing it.
GO_ENV_REPORT=$(with_timeout 120 sbx exec --no-input "$SANITY_SANDBOX" \
    go env GOPROXY GOTOOLCHAIN GOSUMDB GOMODCACHE 2>&1)
say ""
say "go env in the fresh template sandbox (image ENV must survive template save):"
say "$GO_ENV_REPORT"
if printf '%s\n' "$GO_ENV_REPORT" | grep -q 'direct'; then
    say "FATAL: GOPROXY in a template sandbox contains ',direct' — the image ENV"
    say "       did not survive template save, which reopens the v2 env gap."
    exit 1
fi
if ! printf '%s\n' "$GO_ENV_REPORT" | grep -qx 'local'; then
    say "FATAL: GOTOOLCHAIN is not 'local' in a template sandbox — a future go.mod"
    say "       bump would make the VM download a toolchain."
    exit 1
fi

# --------------------------------------------------------------- 8. teardown
if [ "$KEEP" = "1" ]; then
    say "KEEP=1: leaving $SANITY_SANDBOX / $BUILD_SANDBOX running."
else
    run_timed "remove sanity sandbox" sbx rm "$SANITY_SANDBOX" --force || say "WARN: could not remove $SANITY_SANDBOX"
    run_timed "remove build sandbox" sbx rm "$BUILD_SANDBOX" --force || say "WARN: could not remove $BUILD_SANDBOX"
fi

# ------------------------------------------------------------- 9. deliverables
{
    printf '\n## Timings — run %s\n\n' "$RUN_ID"
    printf '| step | ms | result |\n|---|---:|---|\n'
    for row in "${TIMING_ROWS[@]}"; do
        IFS=$'\t' read -r label ms result <<<"$row"
        printf '| %s | %s | %s |\n' "$label" "$ms" "$result"
    done
    printf '\n- go pin: `%s` (go.mod) = `%s` (recipe ARG)\n- pi pin: `%s` (recipe ARG; host `pi --version` was `%s`)\n- base image: `%s` (recipe FROM)\n- model call: %s\n' \
        "$GO_PIN" "$KIT_GO_PIN" "$KIT_PI_PIN" "$PI_PIN" "$BASE_IMAGE" "$MODEL_CALL_STATUS"
} >"$TIMINGS_MD"

{
    date -u +%FT%TZ
    for row in "${TIMING_ROWS[@]}"; do printf '%s\n' "$row"; done
} >>"$TIMINGS_FILE"

say ""
say "================ timings ================"
cat "$TIMINGS_MD" | tee -a "$LOG"
say ""
say "artifacts:"
say "  $TIMINGS_MD"
say "  $LOG"
say "  kit (the artifact; pass the DIR to \`sbx create\`): $KIT_DIR"
say ""
say "templates:"
sbx template ls 2>&1 | tee -a "$LOG"
say ""
say "sandboxes (must be empty):"
sbx ls 2>&1 | tee -a "$LOG"
