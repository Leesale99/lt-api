#!/usr/bin/env bash
# Build the `pi-gate:v1` sbx template from the v3 kit in ./gate-kit.
#
# Run from anywhere (paths are resolved from this script):
#   .pi/sandboxes/build-gate.sh
#
# What this proves, in order: the kit builds; a VM created FROM THE KIT has a
# working git against the mounted repo root, the pinned Go, make, and a Postgres
# server it can start and round-trip; `make audit/static` — the authoritative
# static half of the merge gate — runs green inside that VM; and a COLD-BOOT VM
# created from the saved template repeats all of it. That last point is the whole
# reason the template exists: the host hands a worktree to a gate VM instead of
# executing worktree code itself.
#
# It builds a *machine*, never a verdict: `make audit/static` is run here as an
# end-to-end probe of the image, and its exit code is reported as the gate's
# verdict on the mounted worktree. A red worktree therefore fails this build —
# fix the worktree, then rebuild.
#
# Optional env:
#   KEEP=1               keep the build/sanity sandboxes around for inspection.
#   SKIP_REBUILD=1       reuse an existing pi-gate:v1 template (no rebuild).
#
# Everything the run proves is logged under .pi/sandboxes/dist/logs/.
#
# Long steps (image build, sandbox create, postgres round-trip, audit probe,
# template save) run DETACHED: their output goes to their own step log under
# dist/logs/ and the script polls it, printing a heartbeat every POLL_SECS. A step
# therefore never blocks silently — an agent run that watches this log sees
# progress, and a real hang is bounded by the per-step max instead of an unbounded
# silence.
#
# STAGING (build-only, never committed): `go.mod` + `go.sum` are copied to
# gate-kit/staging/ before the build so the recipe can COPY them into the image
# for the GOMODCACHE warm (a Docker COPY cannot reach outside the build context,
# and the kit must not depend on a path in the repo). The exit trap removes the
# directory on success, failure and interrupt, so no .gitignore entry is needed.
# If a run is killed with SIGKILL, remove it with:
#   rm -rf .pi/sandboxes/gate-kit/staging
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SANDBOXES_DIR="$REPO_ROOT/.pi/sandboxes"
GATE_KIT_DIR="$SANDBOXES_DIR/gate-kit"
DIST_DIR="$SANDBOXES_DIR/dist"
LOG_DIR="$DIST_DIR/logs"
WORKTREE="$REPO_ROOT"
STAGING_DIR="$GATE_KIT_DIR/staging"

BUILD_SANDBOX="pi-gate-build"
SANITY_SANDBOX="pi-gate-sanity"
TEMPLATE_TAG="pi-gate:v1"
CPUS=4
MEMORY=8g
KIT_YAML="$GATE_KIT_DIR/pi-gate.yaml"
KIT_RECIPE="$GATE_KIT_DIR/pi-gate.dockerfile"
SKIP_REBUILD="${SKIP_REBUILD:-0}"
KEEP="${KEEP:-0}"

# Pins echoed in the run log and checked in-VM.
# Go comes from the repo's go.mod (`go` directive) and must match the recipe's
# ARG. There is deliberately NO pi pin: a gate image ships no agent.
GO_PIN=$(awk '$1 == "go" { print $2; exit }' "$REPO_ROOT/go.mod")
KIT_GO_PIN=$(sed -n 's/^ARG GO_VERSION=//p' "$KIT_RECIPE" 2>/dev/null | head -n 1)
BASE_IMAGE=$(sed -n 's/^FROM //p' "$KIT_RECIPE" 2>/dev/null | head -n 1)

RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
LOG="$LOG_DIR/build-gate-$RUN_ID.log"
TIMINGS_FILE="$DIST_DIR/timings-gate.tsv"
TIMINGS_MD="$DIST_DIR/timings-gate-$RUN_ID.md"

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

sandbox_exists() { sbx ls -q 2>/dev/null | grep -qx "$1"; }

# `sbx template inspect` is --cloud-only in v1 ("requires --cloud"), so ask the
# local image store instead and match REPOSITORY:TAG by its last path segment
# ("pi-gate" == "docker.io/library/pi-gate").
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

cleanup() {
    if [ "$KEEP" = "1" ]; then
        say ""
        say "KEEP=1: leaving $BUILD_SANDBOX / $SANITY_SANDBOX in place."
    else
        for name in "$SANITY_SANDBOX" "$BUILD_SANDBOX"; do
            if sandbox_exists "$name"; then
                say "cleanup: removing sandbox $name"
                sbx rm "$name" --force >/dev/null 2>&1 || true
            fi
        done
    fi
    # The staging dir is build-only: it exists so the recipe can COPY the module
    # files into the image, and nothing outside a build should see it.
    if [ -d "$STAGING_DIR" ]; then
        rm -rf "$STAGING_DIR"
        say "cleanup: removed build-only staging dir $STAGING_DIR"
    fi
}

trap cleanup EXIT

say "pi-gate kit build — run $RUN_ID"
say "  repo worktree : $WORKTREE"
say "  kit descriptor: $KIT_YAML (schemaVersion 3, kind: workload)"
say "  kit recipe    : $KIT_RECIPE"
say "  base image    : $BASE_IMAGE (recipe FROM)"
say "  go pin        : $GO_PIN (go.mod) / ${KIT_GO_PIN:-<unset>} (recipe ARG)"
say "  pi pin        : none by design — a gate image ships no agent"
say "  postgres      : apt-baked into the image (template save does not carry /var/lib/docker)"
say "  template tag  : $TEMPLATE_TAG"
say "  layout        : --cpus $CPUS --memory $MEMORY"

if ! command -v sbx >/dev/null 2>&1; then
    say "FATAL: \`sbx\` is not on PATH — this script needs the Docker Sandboxes CLI."
    exit 1
fi
for f in "$KIT_YAML" "$KIT_RECIPE" "$GATE_KIT_DIR/files/pg-up" "$GATE_KIT_DIR/files/pg-down"; do
    if [ ! -f "$f" ]; then
        say "FATAL: missing kit file: $f"
        exit 1
    fi
done
if [ -z "$GO_PIN" ] || [ -z "$KIT_GO_PIN" ] || [ -z "$BASE_IMAGE" ]; then
    say "FATAL: could not resolve pins (go.mod go directive, recipe ARG/FROM lines)"
    exit 1
fi
# The Go pin is hardcoded twice on purpose (repo toolchain + recipe), so a bump
# that lands in only one place is a hard failure here rather than a template
# built with a toolchain the repo does not pin.
if [ "$GO_PIN" != "$KIT_GO_PIN" ]; then
    say "FATAL: go.mod pins Go $GO_PIN but $KIT_RECIPE installs Go $KIT_GO_PIN — bump both"
    exit 1
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

# The worktree is the gate's input, so its state is part of the run record. A
# dirty go.sum is the usual suspect: `go mod download` appends h1: lines that
# `go mod tidy` prunes, and `make audit/static` starts with `go mod tidy -diff`.
# Recorded, not fatal: the audit probe below is what decides.
if command -v git >/dev/null 2>&1 && { [ -d "$WORKTREE/.git" ] || [ -f "$WORKTREE/.git" ]; }; then
    WORKTREE_DIRTY=$(GIT_OPTIONAL_LOCKS=0 git -C "$WORKTREE" status --porcelain 2>/dev/null | wc -l | tr -d ' ')
    say "  worktree state: ${WORKTREE_DIRTY:-?} path(s) modified/untracked (git status --porcelain)"
    if [ "${WORKTREE_DIRTY:-0}" != "0" ]; then
        say "  note: the audit probe runs against this worktree AS IS. If it fails,"
        say "        check \`go mod tidy -diff\` / \`make audit/static\` on the host"
        say "        first — that is a repo verdict, not necessarily an image defect."
    fi
else
    say "  WARN: could not read git state of $WORKTREE (git or .git missing on host)"
fi

# ------------------------------------------------------ 1. stage module files
# The recipe COPYs staging/go.mod + staging/go.sum to warm GOMODCACHE. Staged
# from the repo root, so the cache matches the repo's actual dependency graph.
# The trap removes the directory again; nothing here is committed.
rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR"
for f in go.mod go.sum; do
    if [ ! -f "$REPO_ROOT/$f" ]; then
        say "FATAL: $REPO_ROOT/$f is missing — the GOMODCACHE warm needs both files"
        exit 1
    fi
    cp "$REPO_ROOT/$f" "$STAGING_DIR/$f"
done
say ""
say "staged for the image warm (build artifact, removed on exit):"
if command -v shasum >/dev/null 2>&1; then
    say "  $(cd "$STAGING_DIR" && shasum -a 256 go.mod go.sum | tr '\n' ' ')"
fi

# ------------------------------------------------- 2. validate the v3 kit
# There is no v1/v2 style `sbx kit validate` + `sbx kit pack` flow for a v3
# SOURCE kit: v3 validation IS the image build. The descriptor is echoed with
# `sbx kit inspect` (advisory — if this CLI rejects a v3 source directory, the
# build below is the authority and nothing is lost), then the recipe is built
# once with the sandbox-kit:3 frontend and thrown away (`--output
# type=cacheonly`), so a malformed descriptor or a broken RUN fails here, fast
# and cheap, instead of half way through `sbx create`.
mkdir -p "$DIST_DIR"

if run_timed "kit inspect (v3 descriptor echo)" sbx kit inspect "$GATE_KIT_DIR" --offline; then
    say "  descriptor accepted by this CLI"
else
    say "  WARN: \`sbx kit inspect\` did not accept the v3 source kit — the build"
    say "        step below is the validation authority, so this is not fatal."
fi

if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then
    run_detached "kit build (docker buildx, cacheonly) — v3 validation" 2400 30 \
        docker buildx build -f "$KIT_YAML" --output type=cacheonly "$GATE_KIT_DIR" || {
        say "FATAL: the kit recipe failed to build (descriptor or Dockerfile error)."
        say "       Nothing was snapshotted; fix $KIT_YAML / $KIT_RECIPE and re-run."
        exit 1
    }
else
    say "WARN: docker buildx not available — skipping the standalone kit build."
    say "      \`sbx create\` below still builds the image and fails on a bad kit,"
    say "      it just costs a sandbox-create attempt before the error surfaces."
fi

# ------------------------------------------------ 3. build sandbox from the kit
# The kit DIRECTORY is the first positional: it replaces the `shell` agent token
# of the template create form (`sbx create AGENT|SANDBOX_KIT [PATH...]`), and the
# workspaces follow it. There is no `-t` here — the base image is the recipe's
# FROM, and passing both a template and a kit would ask for two images.
# The repo ROOT (not a subdirectory) is the workspace: `git ls-files` in
# `make audit/static` needs to resolve `.git`, and a linked worktree is handled by
# the caller mounting the main repo root as an extra workspace (see README).
run_detached "create build sandbox (v3 kit, image build)" 2400 20 \
    sbx create --name "$BUILD_SANDBOX" --pull never \
    --cpus "$CPUS" --memory "$MEMORY" "$GATE_KIT_DIR" "$WORKTREE" || {
    say "FATAL: could not create the build sandbox from $GATE_KIT_DIR."
    say "       If the error is about the template/pull policy, the base image"
    say "       ($BASE_IMAGE) is not local and --pull never blocked it; drop"
    say "       --pull never or pre-pull the base image."
    say "       If the error is about the kit positional, add -t $BASE_IMAGE"
    say "       to this create."
    exit 1
}

# -------------------------------------------------------------- 4. in-VM checks
TIMEOUT_SECS=180 run_timed "exec: git resolves the mounted repo" \
    sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" git rev-parse --git-dir

TIMEOUT_SECS=120 run_timed "exec: mounted worktree is at the same absolute path" \
    sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c "test -f '$WORKTREE/go.mod' && pwd && ls -d '$WORKTREE'"

TIMEOUT_SECS=120 run_timed "exec: make --version" \
    sbx exec --no-input "$BUILD_SANDBOX" make --version

TIMEOUT_SECS=120 run_timed "exec: gate binaries present (no agent tooling required)" \
    sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c 'set -eu; \
        for b in git make go gofmt jq curl psql createdb dropdb pg_isready pg-up pg-down; do \
            command -v "$b" >/dev/null 2>&1 || { echo "missing: $b" >&2; exit 1; }; \
        done; \
        echo "all gate binaries present"'

GO_VERSION_IN_VM=$(with_timeout 180 sbx exec --no-input "$BUILD_SANDBOX" go version 2>&1)
say ""
say "go version in the build VM:"
say "$GO_VERSION_IN_VM"
case "$GO_VERSION_IN_VM" in
    *" go${KIT_GO_PIN} "*) say "  ok: matches the recipe ARG (go $KIT_GO_PIN)" ;;
    *)
        say "FATAL: the VM's Go is not the pinned $KIT_GO_PIN — the recipe ARG and the"
        say "       installed tarball disagree; the template must not ship that."
        exit 1
        ;;
esac

TIMEOUT_SECS=120 run_timed "exec: go env (GOPROXY/GOTOOLCHAIN/GOSUMDB/GOMODCACHE)" \
    sbx exec --no-input "$BUILD_SANDBOX" go env GOPROXY GOTOOLCHAIN GOSUMDB GOMODCACHE

TIMEOUT_SECS=120 run_timed "exec: pg_config --version" \
    sbx exec --no-input "$BUILD_SANDBOX" pg_config --version

# The DB half of the gate: start the baked cluster, prove the socket answers, and
# prove the throwaway role can create/drop a database (the spec/e2e suites do
# exactly that per run).
TIMEOUT_SECS=300 run_timed "exec: postgres start + throwaway db round-trip" \
    sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c 'set -eu; \
        pg-up; \
        pg_isready -h "$PGHOST" -p "$PGPORT" -d "$PGDATABASE" -U "$PGUSER"; \
        dropdb --if-exists pi_gate_probe; \
        createdb pi_gate_probe; \
        psql -d pi_gate_probe -v ON_ERROR_STOP=1 -c "select version();" \
             -c "create table t(x int);" -c "insert into t values (1);" -c "select count(*) from t;" \
             -c "drop table t;"; \
        dropdb pi_gate_probe; \
        echo "postgres round-trip ok"'

# GOMODCACHE must be non-empty already: the warm happened at image build, so a
# gate VM's first `go vet` does not touch the network.
MODCACHE_REPORT=$(with_timeout 180 sbx exec --no-input "$BUILD_SANDBOX" /bin/sh -c \
    'du -sh "$(go env GOMODCACHE)"; find "$(go env GOMODCACHE)/cache/download" -name "*.zip" 2>/dev/null | wc -l' 2>&1)
say ""
say "GOMODCACHE in the build VM (size, cached zips):"
say "$MODCACHE_REPORT"
if printf '%s\n' "$MODCACHE_REPORT" | tail -n 1 | grep -qx '0'; then
    say "FATAL: GOMODCACHE is empty in the build VM — the image warm did not land."
    exit 1
fi

# ------------------------------------------- 5. the ultimate probe: static gate
# This is the point of the whole kit: the authoritative static gate runs against
# the mounted worktree INSIDE the VM, and the host only reads the exit status.
say ""
say "Running the authoritative static gate inside the gate VM."
run_detached "exec: make audit/static (authoritative static gate)" 3000 30 \
    sbx exec --no-input -w "$WORKTREE" "$BUILD_SANDBOX" make audit/static || {
    say "FATAL: make audit/static failed inside the gate VM."
    say "       This is the gate's verdict on the mounted worktree, not only an"
    say "       image check. Run \`make audit/static\` on the host and compare:"
    say "       a dirty go.sum (e.g. left behind by a \`go mod download\`) fails"
    say "       \`go mod tidy -diff\`, and a stale docs/spec/coverage.md fails"
    say "       \`make spec/diff\`. Both are repo state, not kit defects."
    say "       The image itself is otherwise proven; no template was saved."
    exit 1
}

# --------------------------------------------------------- 6. snapshot template
# Stop the cluster first: a clean shutdown keeps the snapshot's PGDATA tidy, and
# the server must not be running when the filesystem is snapshotted.
TIMEOUT_SECS=180 run_timed "exec: pg-down" \
    sbx exec --no-input "$BUILD_SANDBOX" pg-down || say "WARN: pg-down returned non-zero"

# Both are slow-ish and quiet (the snapshot prints nothing while it streams the
# filesystem), so poll them like the audit probe.
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
# Cold boot is the proof that the snapshot — not the kit's install phase — carries
# everything a gate needs.
run_detached "create from template (cold boot)" 600 15 \
    sbx create --name "$SANITY_SANDBOX" -t "$TEMPLATE_TAG" --pull never \
    --cpus "$CPUS" --memory "$MEMORY" shell "$WORKTREE" || {
    say "FATAL: could not create a sandbox from $TEMPLATE_TAG"
    exit 1
}

TIMEOUT_SECS=120 run_timed "sanity exec: git resolves the mounted repo" \
    sbx exec --no-input -w "$WORKTREE" "$SANITY_SANDBOX" git rev-parse --git-dir

SANITY_GO_VERSION=$(with_timeout 180 sbx exec --no-input "$SANITY_SANDBOX" go version 2>&1)
say ""
say "go version in the cold-boot VM:"
say "$SANITY_GO_VERSION"
case "$SANITY_GO_VERSION" in
    *" go${KIT_GO_PIN} "*) say "  ok: the template carries the pinned Go ($KIT_GO_PIN)" ;;
    *) say "FATAL: the template's Go is not the pinned $KIT_GO_PIN"; exit 1 ;;
esac

# Image ENV must survive `sbx template save` (v2 kit env did not).
GO_ENV_REPORT=$(with_timeout 120 sbx exec --no-input "$SANITY_SANDBOX" \
    go env GOPROXY GOTOOLCHAIN GOSUMDB GOMODCACHE 2>&1)
say ""
say "go env in the cold-boot VM (image ENV must survive template save):"
say "$GO_ENV_REPORT"
if printf '%s\n' "$GO_ENV_REPORT" | grep -q 'direct'; then
    say "FATAL: GOPROXY in a template sandbox contains ',direct' — the image ENV did"
    say "       not survive template save, which reopens the module-fetch bypass."
    exit 1
fi
if ! printf '%s\n' "$GO_ENV_REPORT" | grep -qx 'local'; then
    say "FATAL: GOTOOLCHAIN is not 'local' in a template sandbox — a future go.mod"
    say "       bump would make the gate VM download a toolchain."
    exit 1
fi
if ! printf '%s\n' "$GO_ENV_REPORT" | grep -qx '/home/agent/go/pkg/mod'; then
    say "FATAL: GOMODCACHE is not the image default in a template sandbox — the warm"
    say "       cache is not where the snapshot captured it."
    exit 1
fi

CACHE_REPORT=$(with_timeout 180 sbx exec --no-input "$SANITY_SANDBOX" /bin/sh -c \
    'du -sh "$(go env GOMODCACHE)"; find "$(go env GOMODCACHE)/cache/download" -name "*.zip" 2>/dev/null | wc -l' 2>&1)
say ""
say "GOMODCACHE in the cold-boot VM (size, cached zips):"
say "$CACHE_REPORT"
if printf '%s\n' "$CACHE_REPORT" | tail -n 1 | grep -qx '0'; then
    say "FATAL: GOMODCACHE came back empty — the snapshot did not capture"
    say "       /home/agent/go/pkg/mod, so the template is not worth shipping."
    exit 1
fi

TIMEOUT_SECS=300 run_timed "sanity exec: postgres start + throwaway db round-trip" \
    sbx exec --no-input "$SANITY_SANDBOX" /bin/sh -c 'set -eu; \
        pg-up; \
        dropdb --if-exists pi_gate_probe; \
        createdb pi_gate_probe; \
        psql -d pi_gate_probe -v ON_ERROR_STOP=1 -c "select 1;" >/dev/null; \
        dropdb pi_gate_probe; \
        echo "postgres round-trip ok (cold boot)"'

run_detached "sanity exec: make audit/static (cold boot from the template)" 3000 30 \
    sbx exec --no-input -w "$WORKTREE" "$SANITY_SANDBOX" make audit/static || {
    say "FATAL: make audit/static failed in a sandbox created from $TEMPLATE_TAG."
    say "       The template does not carry everything the gate needs; inspect the"
    say "       step log above (it is the gate's own output)."
    exit 1
}

# --------------------------------------------------------------- 8. teardown
if [ "$KEEP" = "1" ]; then
    say "KEEP=1: leaving $SANITY_SANDBOX / $BUILD_SANDBOX running."
else
    run_timed "remove sanity sandbox" sbx rm "$SANITY_SANDBOX" --force || say "WARN: could not remove $SANITY_SANDBOX"
    run_timed "remove build sandbox" sbx rm "$BUILD_SANDBOX" --force || say "WARN: could not remove $BUILD_SANDBOX"
fi

# ------------------------------------------------------------- 9. deliverables
{
    printf '\n## Timings — pi-gate run %s\n\n' "$RUN_ID"
    printf '| step | ms | result |\n|---|---:|---|\n'
    for row in "${TIMING_ROWS[@]}"; do
        IFS=$'\t' read -r label ms result <<<"$row"
        printf '| %s | %s | %s |\n' "$label" "$ms" "$result"
    done
    printf '\n- go pin: `%s` (go.mod) = `%s` (recipe ARG)\n' "$GO_PIN" "$KIT_GO_PIN"
    printf -- '- pi pin: none by design (a gate image ships no agent)\n'
    printf -- '- base image: `%s` (recipe FROM)\n' "$BASE_IMAGE"
    printf -- '- postgres: apt-baked into the image; `sbx template save` does not capture /var/lib/docker\n'
    printf -- '- ultimate probe: `make audit/static` green in the build VM AND in a cold-boot VM from `%s`\n' "$TEMPLATE_TAG"
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
say "  kit (the artifact; pass the DIR to \`sbx create\`): $GATE_KIT_DIR"
say ""
say "templates:"
sbx template ls 2>&1 | tee -a "$LOG"
say ""
say "sandboxes (must be empty):"
sbx ls 2>&1 | tee -a "$LOG"
