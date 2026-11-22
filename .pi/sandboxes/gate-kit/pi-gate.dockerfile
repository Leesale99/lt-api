# syntax=docker/dockerfile:1
#
# pi-gate recipe — the image half of the v3 kit (see ./pi-gate.yaml).
#
# A gate VM is a one-shot executor: mount the worktree, run the gate, exit. So
# this image is deliberately the *smallest* thing that can run
# `make audit/static` (git + Go + make + jq) plus a Postgres server for the
# DB-backed gate, and nothing else. There is no `pi`, no npm, no node and no
# model configuration — a gate never calls a provider, and an image that cannot
# run an agent cannot be talked into one.
#
# Two constraints shape the recipe:
#   * `sbx template save` snapshots the sandbox FILESYSTEM, not /var/lib/docker.
#     Postgres therefore cannot be a nested container (DinD is also a bigger
#     surface); the server and a pre-initialised cluster are baked into the image
#     under /home/agent, which the snapshot does capture.
#   * the image ENV survives the snapshot, so GOTOOLCHAIN/GOPROXY/GOSUMDB are set
#     here *and* written into the agent's own `go env` file. A gate VM created
#     from `pi-gate:v1` alone still refuses to download a different toolchain or
#     fall back to direct VCS fetching.
FROM docker/sandbox-templates:shell

# Version pins, in one place. build-gate.sh asserts GO_VERSION against the repo's
# go.mod `go` directive and fails the run on drift. There is intentionally no pi
# pin: the gate image ships no agent.
ARG GO_VERSION=1.27.1

# Image-level environment (survives `sbx template save`):
#   GOTOOLCHAIN  never download a different toolchain than the pinned one
#   GOPROXY      module proxy ONLY — no `,direct`, so module bytes can only come
#                from proxy.golang.org; a proxy outage fails loudly instead of
#                falling back to direct VCS fetching
#   GOSUMDB      one checksum database
#   PGDATA/PGHOST/PGPORT  the baked cluster (see below); PGHOST is the unix
#                socket directory, so a bare `psql` works inside the VM
#   PGUSER/PGDATABASE     the throwaway test role/db, mirroring CI's service
#                container. GOCACHE/GOMODCACHE are deliberately NOT set: they
#                stay at the image defaults (/home/agent/.cache/go-build,
#                /home/agent/go/pkg/mod) so the warm cache lands in the snapshot.
ENV GOTOOLCHAIN=local \
    GOPROXY=https://proxy.golang.org \
    GOSUMDB=sum.golang.org \
    PGDATA=/home/agent/pgdata \
    PGHOST=/home/agent/pgsock \
    PGPORT=5432 \
    PGUSER=lt_test \
    PGDATABASE=lt_api_test

# The base image runs as `agent`; system-level installs need root, the agent's
# own files are written after switching back (same pattern as the official
# custom-template example for docker/sandbox-templates).
USER root

# The gate toolchain, one apt transaction. `postgresql-contrib` is not optional:
# the migrations run `CREATE EXTENSION citext`, which lives in contrib on Debian.
# `libpq-dev` is here for `pg_config` (the recipe, the probes and the pg-up/pg-down
# helpers all locate the server bindir through it). `curl` is explicit so the Go
# tarball step never has to guess what the base image provides. `gcc` (via
# build-essential) is required by `go test -race` in `make audit`: the race
# detector builds with cgo, and cgo without a C compiler fails for an image
# reason, not a tree reason.
#
# BASE-IMAGE ASSUMPTION: `docker/sandbox-templates:shell` is Debian-family with
# apt, an `agent` user (UID 1000) and /home/agent. Both facts are asserted below
# with an actionable message rather than discovered half way through a gate run.
RUN set -eu; \
    if ! command -v apt-get >/dev/null 2>&1; then \
      echo "pi-gate: base image is not apt-based — port this RUN to its package manager" >&2; \
      exit 1; \
    fi; \
    if ! command -v su >/dev/null 2>&1; then \
      echo "pi-gate: no 'su' binary — add the 'login' package to this install list" >&2; \
      exit 1; \
    fi; \
    export DEBIAN_FRONTEND=noninteractive; \
    echo "pi-gate: installing the gate toolchain (git make jq postgresql)"; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl git jq make libpq-dev postgresql postgresql-contrib; \
    rm -rf /var/lib/apt/lists/*; \
    git --version; \
    make --version | head -n 1; \
    jq --version; \
    pg_config --version

# Go from the official tarball, arch-detected — the same install the pi-worker
# recipe uses: /usr/local/go + symlinks in /usr/local/bin. curl is installed
# above, so there is exactly one download path and no node fallback.
RUN set -eu; \
    case "$(uname -m)" in \
      aarch64|arm64) GOARCH=arm64 ;; \
      x86_64|amd64) GOARCH=amd64 ;; \
      *) echo "pi-gate: unsupported machine type $(uname -m)" >&2; exit 1 ;; \
    esac; \
    url="https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz"; \
    echo "pi-gate: installing Go ${GO_VERSION} (${GOARCH}) from ${url}"; \
    tmp="$(mktemp -d)"; \
    curl -fsSL "$url" -o "$tmp/go.tgz"; \
    rm -rf /usr/local/go; \
    tar -C /usr/local -xzf "$tmp/go.tgz"; \
    rm -rf "$tmp"; \
    ln -sf /usr/local/go/bin/go /usr/local/bin/go; \
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
    /usr/local/go/bin/go version

# The pg-up / pg-down helpers. Debian's cluster tooling (`pg_ctlcluster`) insists
# on root; the gate VM runs as `agent`, so the cluster is a plain initdb data dir
# owned by the agent and driven with the server's own `pg_ctl`. The helpers hide
# that (and the socket-dir/port flags) behind two commands the gate runner and
# build-gate.sh can call without knowing the Postgres version.
COPY files/pg-up files/pg-down /usr/local/bin/
RUN chmod 0755 /usr/local/bin/pg-up /usr/local/bin/pg-down

# Postgres, baked: drop the distro's root-owned `main` cluster (it would only add
# ~40 MB of dead weight to every snapshot), initdb a cluster in the agent's home
# as the agent (initdb refuses to run as root), point it at loopback + a unix
# socket under the agent's home, and create the CI-shaped throwaway role/db so a
# DSN identical to CI's works in the VM. The log stays out of the snapshot.
RUN set -eu; \
    id agent >/dev/null 2>&1 || { echo "pi-gate: base image has no 'agent' user (UID 1000)" >&2; exit 1; }; \
    test -d /home/agent || { echo "pi-gate: base image has no /home/agent" >&2; exit 1; }; \
    pgver="$(ls /usr/lib/postgresql 2>/dev/null | sort -V | tail -n 1)"; \
    bindir="/usr/lib/postgresql/$pgver/bin"; \
    if [ ! -x "$bindir/initdb" ]; then \
      bindir="$(pg_config --bindir)"; \
    fi; \
    test -x "$bindir/initdb" || { echo "pi-gate: no initdb under $bindir (is postgresql installed?)" >&2; exit 1; }; \
    pg_dropcluster --stop "$pgver" main >/dev/null 2>&1 || true; \
    rm -rf "$PGDATA" "$PGHOST"; \
    mkdir -p "$PGDATA" "$PGHOST"; \
    chown -R agent:agent "$PGDATA" "$PGHOST"; \
    chmod 700 "$PGDATA"; \
    su agent -s /bin/sh -c "$bindir/initdb -D $PGDATA -U postgres -E UTF8 --locale=C --auth-local=trust --auth-host=trust"; \
    printf '%s\n' \
      "listen_addresses = '127.0.0.1'" \
      "port = $PGPORT" \
      "unix_socket_directories = '$PGHOST'" \
      "unix_socket_permissions = 0700" \
      >> "$PGDATA/postgresql.conf"; \
    su agent -s /bin/sh -c "$bindir/pg_ctl -D $PGDATA -l /tmp/pg-init.log -w start" >/dev/null; \
    su agent -s /bin/sh -c "$bindir/psql -h $PGHOST -p $PGPORT -U postgres -d postgres -v ON_ERROR_STOP=1 \
      -c \"CREATE ROLE lt_test LOGIN SUPERUSER PASSWORD 'test';\" \
      -c \"CREATE DATABASE lt_api_test OWNER lt_test;\""; \
    su agent -s /bin/sh -c "$bindir/pg_ctl -D $PGDATA -w -m fast stop" >/dev/null; \
    rm -f /tmp/pg-init.log "$PGDATA/server.log"; \
    ls -ld "$PGDATA" "$PGHOST"; \
    cat "$PGDATA/PG_VERSION"

USER agent

# Write the same values into the agent's own `go env` file (/home/agent/
# .config/go/env), so `go env GOPROXY GOTOOLCHAIN GOSUMDB` agrees with the image
# ENV even in a context that drops the container's ENV. HOME/GOENV are explicit:
# as a non-root USER, a build context without HOME must not decide where the file
# lands.
RUN set -eu; \
    mkdir -p /home/agent/.config/go; \
    HOME=/home/agent GOENV=/home/agent/.config/go/env \
      go env -w GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOTOOLCHAIN=local; \
    HOME=/home/agent GOENV=/home/agent/.config/go/env \
      go env GOPROXY GOTOOLCHAIN GOSUMDB

# The Go module cache, warmed from a pair of module files staged by
# build-gate.sh (COPY cannot reach outside the build context, so the kit carries
# no copy of the repo). Why this shape rather than warming the mounted worktree
# at sandbox-create time, the way the pi-worker build does:
#   * the slow, network-touching step happens once, at image build, under the
#     kit's install-phase egress — not on every gate VM boot;
#   * `go mod download` rewrites go.sum, and this worktree may be the author's
#     read-write checkout. Downloading in a throwaway /tmp dir leaves the repo
#     byte-for-byte untouched (no snapshot/restore dance needed);
#   * the cache lands at the image default (/home/agent/go/pkg/mod), which the
#     template snapshot captures, so a gate VM starts able to `go vet` and
#     `go tool staticcheck` without a single fetch.
COPY --chown=1000:1000 staging/go.mod staging/go.sum /tmp/gomodwarm/
RUN set -eu; \
    export HOME=/home/agent; \
    echo "pi-gate: warming GOMODCACHE from the staged go.mod/go.sum"; \
    cd /tmp/gomodwarm; \
    HOME=/home/agent go mod download all; \
    cd /; \
    rm -rf /tmp/gomodwarm; \
    du -sh "$(go env GOMODCACHE)"; \
    echo "cached module zips: $(find "$(go env GOMODCACHE)/cache/download" -name '*.zip' | wc -l)"

# Assertion: the gate surface exists, the Go pin is the one the recipe claims,
# the image env is what a gate expects, and the module cache is not empty. A
# template built from a hollow image is worse than a failed build.
RUN set -eu; \
    export HOME=/home/agent; \
    echo "pi-gate: asserting the gate surface"; \
    for bin in git make go gofmt jq curl psql createdb dropdb pg_isready pg_config pg-up pg-down; do \
      command -v "$bin" >/dev/null 2>&1 || { echo "pi-gate: missing required binary: $bin" >&2; exit 1; }; \
    done; \
    git --version; \
    make --version | head -n 1; \
    jq --version; \
    go version; \
    pg_config --version; \
    need_eq() { [ "$1" = "$2" ] || { echo "pi-gate: expected $3=$2, got '$1'" >&2; exit 1; }; }; \
    go version | grep -q " go${GO_VERSION} " || { echo "pi-gate: installed Go is not ${GO_VERSION}" >&2; exit 1; }; \
    need_eq "$(go env GOPROXY)" "https://proxy.golang.org" GOPROXY; \
    need_eq "$(go env GOTOOLCHAIN)" "local" GOTOOLCHAIN; \
    need_eq "$(go env GOSUMDB)" "sum.golang.org" GOSUMDB; \
    need_eq "$(go env GOMODCACHE)" "/home/agent/go/pkg/mod" GOMODCACHE; \
    test -s "$PGDATA/PG_VERSION" || { echo "pi-gate: no baked cluster at $PGDATA" >&2; exit 1; }; \
    test -f "$PGDATA/postgresql.conf"; \
    test -d "$PGHOST"; \
    zips="$(find "$(go env GOMODCACHE)/cache/download" -name '*.zip' | wc -l)"; \
    [ "$zips" -gt 0 ] || { echo "pi-gate: GOMODCACHE is empty — the warm step did nothing" >&2; exit 1; }; \
    if command -v pi >/dev/null 2>&1; then \
      echo "pi-gate: 'pi' must not be in the gate image" >&2; exit 1; \
    fi; \
    for bin in node npm; do \
      if command -v "$bin" >/dev/null 2>&1; then \
        echo "pi-gate: note: the base image ships $bin; no gate step uses it"; \
      fi; \
    done; \
    echo "pi-gate: gate surface ok ($zips module zips warm, cluster at $PGDATA)"
