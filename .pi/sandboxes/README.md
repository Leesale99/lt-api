# pi-worker sandbox kit → `pi-worker:v1` template

This directory builds the **sbx image that delegate worker VMs boot from**: a Go
toolchain, the in-VM `pi` coding agent, its provider/model config, and a warm Go
module cache — all baked into one snapshot so a task sandbox starts ready to
build instead of ready to install.

The kit is written in the **v3 idiomatic shape** (`schemaVersion: "3"`,
`kind: workload`): everything reusable is image content, declared in a
stem-paired Dockerfile recipe, and the descriptor only adds the runtime
capabilities the image cannot express itself (the sbx workload contract, the
network policy). There is no install phase at create time and nothing to pack —
**the kit directory is the artifact**.

It builds *only* the machine. Everything that is per-task or sensitive — the
worktree mount, egress rules, the model key — is attached when a sandbox is
*created*, never captured in the snapshot (see "Secrets" and "Egress" below).

The same directory also builds `pi-gate:v1`: an agent-less image (git, Go, make,
Postgres, no `pi`) for the **authoritative gate VM** — see "pi-gate kit" below.

```
.pi/sandboxes/
├── kit/
│   ├── pi-worker.yaml         v3 descriptor: schemaVersion 3, kind: workload,
│   │                          dockerfile:, capabilities (sbx@1 + network-policy@1)
│   ├── pi-worker.dockerfile   the recipe: FROM shell-docker, ENV, RUN go/npm, COPY pi config
│   └── files/
│       ├── models.json        opencode-go provider catalog (COPY'd into the image)
│       ├── settings.json      pi defaults + the npm extension packages pi loads (COPY'd)
│       └── web-search.json    web-search provider for the researcher (COPY'd into the image)
├── relay/
│   └── codex-search-relay.mjs host-side codex relay: the researcher's web-search backend
├── gate-kit/
│   ├── pi-gate.yaml           v3 descriptor: the locked-down gate executor (no agent)
│   ├── pi-gate.dockerfile     the recipe: FROM shell, git/Go/make/Postgres, warm GOMODCACHE
│   └── files/
│       ├── pg-up              start the image-baked Postgres (idempotent, health-checked)
│       └── pg-down            stop it cleanly before a snapshot
├── build.sh                   the build pipeline (validate → build → warm → snapshot → prove → clean)
├── build-gate.sh              the gate-template pipeline (probe → audit/static → snapshot → cold boot)
├── README.md                  this file
└── dist/                      artifacts, gitignored: timings*.tsv, timings-*.md, logs/
```

Templates produced here:

| template | built by | what it is |
|---|---|---|
| `pi-worker:v1` | `./build.sh` | Go + pi + provider/config + warm module cache — the delegate worker VMs |
| `pi-gate:v1` | `./build-gate.sh` | git + Go + make + Postgres, **no agent**, 3-host runtime egress — the authoritative gate VM |

## What the snapshot contains

| item | detail |
|---|---|
| base image | `docker/sandbox-templates:shell-docker` (recipe `FROM`, agent UID 1000, Node/npm, sandbox proxy plumbing) |
| Go | `1.27.1` at `/usr/local/go`, symlinked to `/usr/local/bin/go` and `gofmt` (`ARG GO_VERSION`) |
| pi | `@earendil-works/pi-coding-agent@1.6.0`, global npm install (`ARG PI_VERSION`) |
| image env | `GOTOOLCHAIN=local`, `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `PI_AGENT_HOME=/home/agent/.pi/agent` (`ENV`) — plus the same three Go values in `/home/agent/.config/go/env` |
| pi config | `/home/agent/.pi/agent/models.json` (opencode-go provider catalog) + `settings.json` (default `opencode-go` / `glm-5.3` / thinking `high`, plus the `packages` list below) + `web-search.json` (provider `openai` with `openaiResponsesUrl` pointing at the host codex relay, the mountless researcher's web-search backend), all `COPY --chown=1000:1000` |
| pi extension packages | `/home/agent/.pi/agent/npm/` — an npm project (`{"name":"pi-extensions","private":true,"dependencies":{…}}`, **exact** pins) with `node_modules` pre-installed at build time: `pi-web-access@0.48.0` (`web_search`, `fetch_content`, `source_check`, `get_search_content`) and `@upstash/context7-pi@0.1.3` (`resolve-library-id`, `query-docs`). `settings.json` declares them as `npm:pi-web-access` / `npm:@upstash/context7-pi` (name only — the image owns the version), so pi loads the baked copy instead of reinstalling: pi only reinstalls a package whose installed version fails the configured range, and an unversioned entry accepts any installed version. |
| Go module cache | `/home/agent/go/pkg/mod`, ~132 MB / 45 module zips (this repo's `go.mod` graph) |
| template tag | `pi-worker:v1` |

Nothing auto-runs on boot: the recipe sets no entrypoint (the base image's
shell-compatible launcher keeps the sandbox alive), so the delegate extension
starts `pi` itself through `sbx exec`.

## pi-gate kit → `pi-gate:v1` template (the authoritative gate VM)

The pi-worker template answers "where does an agent run". This one answers
"where does the **gate** run". `WORKFLOW.md` settles that question the other way
from the host-passthrough era: *"the host never executes worktree code, only `git`
(the integrity check) and `sbx exec`"*. Before `pi-gate:v1`, `make audit/static`
executed the worktree's Makefile, Go toolchain and `cmd/spec` on the host itself,
and the verdict inherited the host's own trust.

`pi-gate:v1` moves that execution into a VM. The worktree root is mounted into a
locked-down sandbox, `make audit/static` runs **there**, and the host only reads an
exit code and the output. Worktree code is data to the host, never a process it
spawned. The baked Postgres means the DB-backed `make audit` runs there too, with
its DSN passed as a make command-line override (see the scope note at the end of
this section).

The image is deliberately the smallest thing that can run a gate — `git`, Go,
`make`, `jq`, a Postgres server — and nothing else: **no `pi`, no npm, no node, no
model config**. A gate does not think, commit, or call a provider, and an image
that cannot run an agent cannot be talked into one.

### What the `pi-gate:v1` snapshot contains

| item | detail |
|---|---|
| base image | `docker/sandbox-templates:shell` — the non-Docker flavor; a gate needs no nested daemon |
| gate toolchain | `git`, `make`, `jq`, `curl`, `ca-certificates` (apt, one transaction) |
| Go | `1.27.1` at `/usr/local/go`, symlinked to `/usr/local/bin/go` and `gofmt` (`ARG GO_VERSION`) |
| Postgres | Debian's `postgresql` + `postgresql-contrib` (+ `libpq-dev` for `pg_config`), with a **pre-initialised cluster** at `/home/agent/pgdata`: superuser role `lt_test` / password `test`, database `lt_api_test`, loopback TCP `127.0.0.1:5433`, unix socket `/home/agent/pgsock` |
| image env | `GOTOOLCHAIN=local`, `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `PGDATA`/`PGHOST`/`PGPORT`/`PGUSER`/`PGDATABASE` — plus the same three Go values in `/home/agent/.config/go/env` |
| Go module cache | `/home/agent/go/pkg/mod`, ~132 MB / 45 module zips, warmed **at image build** from a staged `go.mod`+`go.sum` |
| helpers | `/usr/local/bin/pg-up` (start + `pg_isready`, idempotent) and `/usr/local/bin/pg-down` |
| capability set | `com.docker.sandbox/sbx@1` + `network-policy@1`. No lifecycle, no long-running, no host-mount, no git-identity: a gate is a one-shot executor that never commits |
| template tag | `pi-gate:v1` |

Nothing auto-starts. Postgres is baked but *stopped* in the snapshot; the gate
runner calls `pg-up` (the descriptor has no lifecycle capability by design, so
there is no boot hook to hide the start in), then points the DB-backed gate at

```
postgres://lt_test:test@127.0.0.1:5433/lt_api_test?sslmode=disable
```

Same role, password and database name as CI's `postgres:18` service container and
`.envrc`'s compose DSN. The port is the repo's documented `5433` (image ENV
`PGPORT`, written into `postgresql.conf` at build) — i.e. `.envrc`'s DSN verbatim:
the gate VM has no host Postgres to avoid, so it can use the documented port rather
than CI's `5432` service port.

### Workspace model: the repo ROOT, or git cannot resolve

The sandbox is created with the **repository root** as its workspace positional:

```sh
sbx create <this dir>/gate-kit <repo-root>
```

That is not a convenience. `make audit/static` starts with `go mod tidy -diff` and
its gofmt step runs `git ls-files '*.go'`, so the VM's git must resolve `.git`;
mounting a subdirectory (or the kit directory alone) leaves `git rev-parse
--git-dir` with nothing to find. The build verifies exactly that inside the VM.

The gate itself does not need to *write* to that mount: `go mod tidy -diff` only
reports, `spec gen --diff` never writes (`cmd/spec/gen.go` says so explicitly),
and every cache (`GOCACHE`, `GOMODCACHE`) lives in the image's home directory. A
gate VM can therefore mount the tree read-only — worth pairing with
`GIT_OPTIONAL_LOCKS=0`, which is how the in-VM git probe was validated — and the
kit works either way. Making the mount read-only is the extension's decision, not
the image's.

**Linked worktrees are the extension's job, not this kit's.** In a `git worktree`
checkout `.git` is a *file* pointing into the main repository's
`.git/worktrees/<name>`, so a mount of the linked tree alone does not carry the
target. The delegate extension mounts the **main repo root read-only as an extra
workspace** alongside the linked worktree when it creates a gate VM; this kit only
supplies the image, and deliberately declares no host-mount capability so the
policy stays in one place (the extension's profile table).

### Why Postgres is baked into the image (and not DinD)

`sbx template save` snapshots the sandbox **filesystem**; it does not capture the
nested Docker daemon's `/var/lib/docker` (verified by probe on the sbx version this
repo targets). Anything a nested container writes at runtime is therefore absent
from the template, so a Compose-provisioned database — the `make db/up` flow —
would have to be created and populated on every gate VM boot, and the VM would
need a Docker daemon inside it (`shell-docker`, plus the daemon's own surface).

So the recipe installs the server with apt and `initdb`s a cluster **inside
`/home/agent`**, which the snapshot does capture. The trade is honest: the VM's
Postgres is Debian's packaged server (15 on bookworm, 17 on trixie), while CI and
`compose.yaml` run `postgres:18`. The static gate never touches it; the DB-backed
gate runs the repo's migrations, and if that SQL ever depends on an 18-only
feature this is the first place to look.

The `pg_ctlcluster` wrapper Debian ships insists on root. The gate VM runs as
`agent`, so the cluster is a plain data directory owned by the agent and driven
with the server's own `pg_ctl` — wrapped in `pg-up`/`pg-down` so no gate command
has to know the Postgres version or carry flags.

### Go module cache: warm at build, three hosts at runtime

The warm happens in the **image**, not at sandbox-create time: `build-gate.sh`
stages `go.mod`+`go.sum` into `gate-kit/staging/`, the recipe `COPY`s that pair to
`/tmp/gomodwarm` and runs `go mod download all` as `agent`, then throws the
directory away. `GOMODCACHE` is left at the image default (`/home/agent/go/pkg/mod`)
precisely so the snapshot captures it (same reasoning as the pi-worker warm).

Why not warm the mounted worktree at create time, the way `build.sh` does? Because
`go mod download` **rewrites `go.sum`**, and a gate VM's mount may be the author's
live checkout. Warming from a COPY'd pair inside a throwaway `/tmp` directory
leaves the repository byte-for-byte untouched — no snapshot/restore dance, and no
chance of a gate run dirtying the tree it is grading.

The kit's `runtime.allow` list is then only the fallbacks a cache miss needs:

```
proxy.golang.org:443, sum.golang.org:443, storage.googleapis.com:443
```

No model host (there is no agent), no npm, no apt, no VCS fallback — `GOPROXY` has
no `,direct`, for the same reason the pi-worker image omits it: direct fetching
would be a bypass route around the allowlist, not a feature. If a worktree's
`go.mod` has moved ahead of the image, those three hosts serve the delta; anything
else fails closed.

### Build it

```sh
./.pi/sandboxes/build-gate.sh     # ~5-10 min: two full static gates, one image build
```

| env | default | meaning |
|---|---|---|
| `KEEP` | `0` | `1` keeps `pi-gate-build` and `pi-gate-sanity` around for inspection. |
| `SKIP_REBUILD` | `0` | `1` exits early if `pi-gate:v1` already exists. |

Pipeline (timings in `dist/timings-gate.tsv` + `dist/timings-gate-<run>.md`):

0. **pins + pre-clean** — the Go pin is read from `go.mod` and from the recipe's
   `ARG GO_VERSION`; drift is fatal. There is **no pi pin**: a gate image ships no
   agent, so there is nothing to cross-check.
1. **stage module files** — `go.mod`+`go.sum` copied to `gate-kit/staging/`. This
   is a build-only artifact: the exit trap removes it on success, failure and
   interrupt, so nothing needs a `.gitignore` entry. (A `SIGKILL`ed run leaves it
   behind: `rm -rf .pi/sandboxes/gate-kit/staging`.)
2. **validate the v3 kit** — `sbx kit inspect` (advisory) plus
   `docker buildx build -f gate-kit/pi-gate.yaml --output type=cacheonly`, the real
   validation: a bad descriptor or a failing `RUN` dies here.
3. **build VM** — `sbx create --name pi-gate-build <gate-kit> <repo-root>`, i.e. the
   kit directory is the `AGENT|SANDBOX_KIT` positional and the repo root is the
   workspace. This is the only step with install-phase egress (apt + the Go tarball
   + the module warm).
4. **probes** — `git rev-parse --git-dir` in the mounted tree, `make --version`,
   the gate binaries, `go version` **asserted equal to the ARG**, `pg_config
   --version`, `go env` values, and a full Postgres round-trip (`pg-up` →
   `pg_isready` → create/drop a throwaway DB → drop it). `GOMODCACHE` must be
   non-empty; an empty cache fails the run.
5. **the ultimate probe** — `make audit/static` **inside the VM**, against the
   mounted worktree. Exit 0 is required.
6. **snapshot** — `pg-down`, `sbx stop`, `sbx template save pi-gate:v1` (the old
   tag is dropped only now, so a failed run leaves a usable template behind).
7. **cold boot from the template** — a new sandbox from `pi-gate:v1` repeats the
   git probe, the Go pin check, the `go env` assertions, the module-cache check,
   the Postgres round-trip **and `make audit/static` again**. That is the proof the
   snapshot — not the kit's install phase — carries everything a gate needs.
8. **teardown + deliverables** — both sandboxes removed, timings written, template
   and sandbox lists printed.

One caveat, stated up front: the build **fails if the worktree is red for
`make audit/static`**, because running that gate is a pipeline step. That is the
gate's verdict on the worktree, not an image defect. The usual false alarm is a
`go.sum` dirtied by someone's `go mod download` (`go mod tidy -diff` is the first
step of the gate); the script prints `git status --porcelain` and a pointer to the
host command before it starts, so the distinction is visible in the log.

Scope note: the *build pipeline* proves the **static** gate only — that is the kit's
scope, not the extension's. Postgres is baked and round-tripped, and the DB-backed
`make audit` is wired: it runs in the gate VM with `LT_API_TEST_DSN` handed to make
as a command-line variable pointing at the VM's own Postgres (a plain env var would
lose to the Makefile's `-include .envrc`). Worker sandboxes are still DSN-less, so
the old rule stands for them: *"A worker that needs a host … is a blocked, explained
report"*.

### Assumptions the host build must watch

- **Base image is apt-based Debian with an `agent` user (UID 1000) and
  `/home/agent`.** Both are asserted in the recipe with actionable messages; if the
  `shell` flavor ever changes, the build says so rather than failing obscurely.
- **apt sources.** `install.allow` lists `deb.debian.org` and
  `security.debian.org` on both `:443` and `:80` — Debian's default `sources.list`
  is plain `http://`, and the port pair costs nothing while a missing `:80` would
  break the build. If the base is https-only, drop the `:80` entries.
- **Postgres version.** Debian's packaged server, not 18 (see above).
- **First-compile cost.** The image warms `GOMODCACHE`, not `GOCACHE`, so the first
  `go tool staticcheck` in a gate VM compiles the analyzer once (tens of seconds).
  A gate VM is short-lived, so warming `GOCACHE` would buy little.

## Sandbox profiles: what a task VM actually gets

One profile table (`.pi/extensions/delegate/sbx.ts`, `SANDBOX_PROFILES`) is the
policy. A role gets exactly what its row lists — no mount it does not name, no
egress it does not name, no secret it does not name. The profile also states the
VM's resources; **no role relies on an sbx default** (`--cpus 0` means *all* host
CPUs, and an omitted `--memory` means 50% of host RAM — both wrong for a task
VM).

| role | workspace mounts | egress allowlist (`sbx policy allow network`) | secret sentinels | vCPUs | memory |
|---|---|---|---|---|---|
| `worker` | worktree `rw` | `opencode.ai`, `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` (:443) | `OPENCODE_GO_API_KEY` | 4 | 8g |
| `general` | worktree `rw` | same four as `worker` | `OPENCODE_GO_API_KEY` | 4 | 8g |
| `scout` | scratch `rw` + worktree `ro` | `opencode.ai` (model only) | — | 2 | 4g |
| `librarian` | vault `rw` + worktree `ro` | `opencode.ai` (model only) | — | 2 | 4g |
| `researcher` | **none (mountless)** | `opencode.ai`, `*.opencode.ai`, `localhost:8787` (host codex relay), `context7.com` (:443) | `CONTEXT7_API_KEY` | 2 | 4g |

Read-only/mountless roles (scout, librarian, researcher) are model-call-bound,
not build-bound, hence 2 CPU / 4g; worker and general are the roles that run Go
builds and gates and keep 4 CPU / 8g.

The **researcher is sandboxed and genuinely mountless**: `sbx create` gets zero
workspace positionals, so the VM has no copy of the repo, the vault, or the
host. Its whole work surface is the network — the model provider, the **host
codex search relay** (`localhost:8787`, which serves `web_search`) and the
docs-lookup backend (`context7.com`) — which is why its agent frontmatter drops
`read` and its prompt says a task needing repo/local access is a blocked report.
Anything a `fetch_content` call reaches for outside that allowlist is denied by
the default-deny proxy.

Its **toolset comes from the image, not from pi's core binary**: `web_search`,
`fetch_content`, `source_check` and `get_search_content` are `pi-web-access`
resources, and `resolve-library-id`/`query-docs` are `@upstash/context7-pi`
resources (see “pi extension packages” above). A kit that ships pi without those
two packages gives the researcher a frontmatter naming tools that do not exist —
the host only has them because its own `settings.json` declares the same npm
packages.

Two fail-closed details of the spawn path:

- `sbx create --no-input` (a global sbx flag, like `sbx exec --no-input`). The
  host process is never interactive, so an unmatched egress request or any other
  prompt must ERROR instead of hanging a spawn that would never produce a
  verdict.
- `--deny-network <host>` at create time, from an optional per-profile
  `denyNetworkHosts` list. Create-time denies are in force *before* the
  per-sandbox allow rule (a separate, later `sbx policy allow` call), which
  closes the post-create window. Every profile's list is currently empty; the
  mechanism is the deliverable, not an invented deny.

### Host-side secret setup (once per host)

Only the model key needs setup as a **custom secret**: the value stays in the
host secret store, the VM only ever sees a `sbx-cs-` placeholder in the env var,
and the sbx proxy substitutes the real value in the HTTP header on requests to
the allowed hosts. Never pass a real key through `-e`, an image `COPY`, or a file
in the VM.

```sh
# model key — every role that runs pi (worker/general/scout/librarian/researcher)
printf '%s' "$OPENCODE_GO_API_KEY" | sbx secret set-custom \
    --host opencode.ai --host '**.opencode.ai' \
    --env OPENCODE_GO_API_KEY --value-stdin
```

**`CONTEXT7_API_KEY` needs NO setup.** A *global* custom secret targeting
`context7.com` already exists on this host (verified live: the placeholder env
auto-injects into every VM), so `resolve-library-id` / `query-docs` work as soon
as the sandbox gets its `context7.com:443` egress rule. Do not re-create it, and
do not pass a real key with `-e`.

**The `OPENAI_API_KEY` custom secret is RETIRED.** The old web-search path
stored a pay-as-you-go OpenAI platform key host-side
(`sbx secret set-custom --host api.openai.com --env OPENAI_API_KEY`), injected
the placeholder into the researcher VM, and called `api.openai.com`. That whole
path is gone: `web_search` now goes through the host **codex search relay** (see
“Codex search relay” below), which spends the host's ChatGPT/codex subscription
and keeps the credential on the host. Do not re-create that custom secret — the
researcher profile no longer lists `api.openai.com` in its egress row nor
`OPENAI_API_KEY` among its sentinels, so nothing could authenticate against it.
If a stale secret still exists on the host, `sbx secret rm --host api.openai.com`
clears it.

The model key is the `opencode-go` key from the host's pi auth
(`~/.pi/agent/auth.json`). `models.json` stores
`"apiKey": "$OPENCODE_GO_API_KEY"`, so pi inside the VM resolves that env var
and the proxy turns the placeholder into the real key on the wire.

Both are **global** secrets (no `--sandbox`), so every sandbox receives the
placeholder. That is safe because the placeholder is useless without an allow
rule: a role whose egress list lacks `opencode.ai` or `context7.com` cannot send
it anywhere. Check the injection without printing the value:

```sh
sbx exec <sandbox> printenv OPENCODE_GO_API_KEY | head -c 8   # sbx-cs-
sbx exec <sandbox> printenv CONTEXT7_API_KEY | head -c 8      # sbx-cs-
```

## Codex search relay (host-side web search)

The researcher's `web_search` spends the host's **ChatGPT/codex subscription**
— not pay-as-you-go API credits — through a small host process:

```
VM pi-web-access ──POST──> sbx egress localhost:8787 ──> codex-search-relay.mjs
                                                           │ reads ~/.pi/agent/auth.json
                                                           ▼
                                      https://chatgpt.com/backend-api/codex/responses
```

- `.pi/sandboxes/relay/codex-search-relay.mjs` (zero dependencies, Node ≥ 18)
  binds **127.0.0.1 only** and is the **only** thing allowed to talk to
  `chatgpt.com`. `chatgpt.com` never enters a VM, and no OpenAI credential ever
  enters one either: the credential is the host's own `openai-codex` OAuth entry
  in `~/.pi/agent/auth.json`, read **per request**.
- The VM points at it through the baked `web-search.json`
  (`openaiResponsesUrl: http://host.docker.internal:8787/responses`); the sbx
  proxy rewrites `host.docker.internal` to the host loopback, so the
  researcher's egress row is exactly **`localhost:8787`**.
- `openaiApiKey: "relay-managed"` is a dummy. `pi-web-access` requires *some*
  client-side credential value; the relay strips it and substitutes the real
  OAuth bearer upstream. Nothing else reads it.

Run it on the host (the host pi must have signed in to codex at least once):

```sh
node .pi/sandboxes/relay/codex-search-relay.mjs                          # 127.0.0.1:8787
CODEX_RELAY_PORT=9000 node .pi/sandboxes/relay/codex-search-relay.mjs     # other port
```

If you move the port, move `SANDBOX_RELAY_EGRESS` in
`.pi/extensions/delegate/sbx.ts` (and the baked `web-search.json`) with it. Keep
it running for the lifetime of a research delegation. One line per request is
logged — timestamp, method, upstream status, ms — never bodies or tokens.

**Fail-closed expiry.** The relay does **not** refresh the token: refresh
rotation is single-use, so refreshing here could invalidate the host pi
session's stored credential. Instead it re-reads `auth.json` on every request
and answers **503** (short JSON, naming the fix) when the `openai-codex` entry
is missing, is not `oauth`, has no `accountId`, or expires in under two minutes.
The message says to **run a host pi session to refresh**; that session rewrites
the shared file and the next relay request picks it up — no relay restart
needed. An upstream that does not answer within 90 s is a **502**; an upstream
HTTP status is passed through verbatim with its body piped.

**Known limit:** the relay is a host process. A researcher task run while the
host is not running it gets a 503 from the relay and reports web search as
blocked — it does not retry.

## Rebuild

```sh
./.pi/sandboxes/build.sh          # ~3 min on an M-series host, from scratch
```

Run it from anywhere; paths resolve from the script. End state is always
`sandbox ls` empty and `template ls` showing `pi-worker:v1`.

| env | default | meaning |
|---|---|---|
| `KEEP` | `0` | `1` keeps `pi-worker-build` and `pi-worker-sanity` around for inspection. |
| `MODEL_CALL` | `1` | `0` skips the in-VM live model call (the only step that costs tokens). |
| `SKIP_REBUILD` | `0` | `1` exits early if `pi-worker:v1` already exists (a ~1 s no-op on a warm host). |

Pipeline (each step's wall-clock is recorded in `dist/timings.tsv` and
`dist/timings-<run>.md`):

0. **pins + pre-clean** stale build/sanity sandboxes. The script reads the Go
   pin from `go.mod` and from the recipe's `ARG GO_VERSION` (they must match,
   or the run aborts), and prints the pin the recipe will ship next to the host
   `pi --version`. The old `pi-worker:v1` tag is left alone until snapshot time,
   so a failed run never destroys a usable template.
1. **validate the v3 kit** — there is no `sbx kit validate`/`sbx kit pack` for a
   v3 *source* kit. `sbx kit inspect` echoes the descriptor (advisory), and then
   `docker buildx build -f kit/pi-worker.yaml --output type=cacheonly .` is the
   real validation: the `docker/sandbox-kit:3` frontend parses the YAML, and a
   bad descriptor or a failing `RUN` dies here instead of half way through
   `sbx create`. If `docker buildx` is unavailable the step degrades to a
   warning (create still fails on a bad kit, it just gets there later).
2. **create** the build sandbox from the kit — `sbx create <kit-dir> <worktree>`,
   i.e. the kit directory takes the **first positional** (the agent slot,
   `AGENT|SANDBOX_KIT`), replacing the `shell` token used by the template form.
   No `-t`: the base image is the recipe's `FROM`. This is where the image is
   built, and it is the only step that needs build-time (install-phase) egress.
3. **in-VM checks** — `go version`, `pi --version`, worktree mounted at the same
   absolute path, `OPENCODE_GO_API_KEY` placeholder prefix.
4. **warm `GOMODCACHE`** — `go mod download all` in the worktree.
5. **live model call** — `pi -p --model opencode-go/glm-5.3 "reply OK"` through
   the host proxy. Cheap, and the only end-to-end proof that sentinel key +
   `models.json` + egress line up. A failure is logged, not fatal.
6. **stop + `sbx template save`** → `pi-worker:v1` (the filesystem snapshot,
   module cache and image env included).
7. **fresh create from the template** — a cold-boot sandbox is created from
   `pi-worker:v1` (`sbx create -t pi-worker:v1 shell <worktree>`) and re-probed:
   `go version`, `pi --version`, key prefix, baked pi config **and** a non-empty
   `GOMODCACHE` (an empty cache fails the run: the template would be pointless).
   The Go env is now asserted, not just printed: a template sandbox whose
   `GOPROXY` contains `,direct`, or whose `GOTOOLCHAIN` is not `local`, or whose
   `PI_AGENT_HOME` is unset, **fails the build**.
8. **teardown** — both sandboxes removed.
9. **deliverables** — timings written, template and sandbox lists printed.

Long, quiet steps (kit build, sandbox create, module warm, model call, snapshot,
create from template) run **detached**: output goes to
`dist/logs/step-*-<run>.log`, the script polls it and prints a heartbeat, and a
per-step budget bounds the wait. That keeps the run from ever sitting silent for
minutes — a silent pipe is what got an earlier run killed by a stall watchdog,
and the same silence also delays the visible output of a short step, which is
why the timing helper never lets its timeout watcher inherit the log pipe.

`bash -n` is enough to check an edit, but the script is normally validated by
running it, since the whole point is the live proof in step 7.

### Version pins

| pin | where | asserted by |
|---|---|---|
| Go `1.27.1` | repo root `go.mod` (`go` directive) **and** `ARG GO_VERSION=` in `kit/pi-worker.dockerfile` | build.sh aborts if the two disagree |
| pi `1.6.0` | `ARG PI_VERSION=` in `kit/pi-worker.dockerfile` | printed next to the host `pi --version`; a mismatch is a WARN (the host pin is only a cross-check) |
| base image | `FROM docker/sandbox-templates:shell-docker` in the recipe | pinned by local tag (`--pull never`) |
| template | `pi-worker:v1` | the tag the delegate extension resolves as `docker.io/library/pi-worker:v1` |

Bump the Go pin in both places together. Each rebuild may leave the previous
assembled image behind (housekeeping: `sbx template ls`, then
`sbx template rm <IMAGE-ID>` for orphans — the tag form 404s; never remove the
image ID `pi-worker:v1` points at).

## Behavior deltas vs. the v2 kit (deliberate)

The v2 kit was `schemaVersion: "2"`, `kind: sandbox`, with `setup.install`,
`setup.files` and `environment.variables` applied **per sandbox at create
time**. Moving to v3 idiomatic changes four observable behaviors:

1. **Env survives the snapshot (gap closed).** `GOTOOLCHAIN`, `GOPROXY`,
   `GOSUMDB` and `PI_AGENT_HOME` are image `ENV` (plus the Go values written into
   `/home/agent/.config/go/env`), so a sandbox created from `pi-worker:v1` alone
   reports the strict values. Previously a template-only sandbox reported Go's
   defaults (`https://proxy.golang.org,direct` / `auto`) and had no
   `PI_AGENT_HOME`; step 7 above was the evidence, and it now asserts the fix.
2. **Config files are not reset per boot.** `files/models.json` and
   `files/settings.json` are `COPY`'d into the image instead of being written by
   `setup.files` at create time. Consequence: if a worker edits
   `~/.pi/agent/*.json` inside its sandbox, the edit sticks for that sandbox's
   lifetime instead of being overwritten at the next create. Nothing in the
   delegate flow writes those files, so this is a widening of what a worker can
   do, not a change the normal path depends on.
3. **No install-phase egress at create time.** Go and pi are installed by `RUN`
   during the image build, so the network policy's `install.allow` list serves
   the *build* only; a sandbox created from the kit or the template runs with
   `runtime.allow` alone. In v2 every sandbox created with the kit carried the
   install-time allowlist (npm, go.dev, dl.google.com) for its whole life.
4. **The ZIP flow is gone.** `sbx kit pack`/`dist/pi-worker.zip` were the v2
   distribution flow and were never used at runtime: the delegate extension's
   fallback creates from the kit **directory**. Dropping the artifact removes
   dead code from a gitignored output directory.

## Why the Go module cache is warmed

`sbx template save` snapshots the **sandbox filesystem**, so anything written
into the agent's home is inside the template. The image therefore deliberately
does *not* set `GOCACHE`/`GOMODCACHE`: they stay at the image defaults
(`/home/agent/.cache/go-build`, `/home/agent/go/pkg/mod`) so the warmed cache
lands in the snapshot instead of in some path the snapshot misses.

Consequence: a task sandbox created from `pi-worker:v1` already holds every
module of this repo's `go.mod` graph (45 zips, ~132 MB). `go build ./...` works
without a single fetch (observed: 8.7 s on a cold sandbox, module fetch only from
the cache). Without the warm cache every worker VM would start by re-downloading
the dependency graph through the egress proxy — slow, chatty, and a failure mode
when the network is having a day.

Only `GOMODCACHE` is warmed; `GOCACHE` (compiled artifacts) is not, so the first
build in a task sandbox still compiles dependencies once.

**The warm step is not read-only.** The worktree is mounted read-write in the
sandbox, and `go mod download` writes `go.sum`: it records `h1:` hashes for the
whole module graph, which `go mod tidy` then prunes (measured: 18 lines, no
go.mod change). Left alone that dirties the author's checkout and fails
`go mod tidy -diff` in `make audit/static`. `build.sh` therefore snapshots
`go.mod`/`go.sum` on the host before the warm and restores them afterwards,
logging the `go.sum` sha256 before/after — the module cache stays warm, the
checkout stays clean. Any build run by hand in a sandbox from this image will do
the same thing to `go.sum`; that is normal Go `-mod=mod` behaviour, not a kit
bug, but it is worth a `go mod tidy -diff` before committing.

## Why `GOPROXY` has no `,direct` fallback

The recipe sets `GOPROXY=https://proxy.golang.org` — no `,direct`. `,direct` is
the escape hatch that makes the go command fall back to **direct VCS fetching**
(git/ssh to arbitrary hosts) when the proxy fails. That would be:

- a bypass route around the egress allowlist — the whole point of the list is
  that module bytes only ever come from `proxy.golang.org` / `sum.golang.org`;
- an extra guaranteed failure (egress denies those hosts) that buries the real
  error under retries.

Proxy-only keeps the network contract exact: if the proxy is down, module
fetches fail loudly and immediately. `GOSUMDB=sum.golang.org` and
`GOTOOLCHAIN=local` complete the same story — no unexpected toolchain downloads,
only one verified checksum database.

Unlike the v2 kit, these values are **image-level**, so they are present in a
sandbox created from `pi-worker:v1` alone:

```
$ sbx exec pi-worker-sanity go env GOPROXY GOTOOLCHAIN GOSUMDB
https://proxy.golang.org
local
sum.golang.org
$ sbx exec pi-worker-sanity printenv PI_AGENT_HOME
/home/agent/.pi/agent
```

(The v2 run of this same probe printed `https://proxy.golang.org,direct` /
`auto` and no `PI_AGENT_HOME`, because kit `environment.variables` are
sandbox-scoped and a filesystem snapshot cannot carry environment. That is the
gap the `ENV` + `go env -w` + step-7 assertion close.)

## Secrets: the template contains none

The snapshot is a long-lived, shared artifact, so no credential may enter it.

- `kit/files/models.json` stores `"apiKey": "$OPENCODE_GO_API_KEY"` — a
  *sentinel*, not a key.
- `kit/files/web-search.json` stores no key at all: the provider is `openai`, the
  endpoint is the host relay, and `openaiApiKey: "relay-managed"` is a dummy the
  relay replaces upstream. The credential lives on the host, in
  `~/.pi/agent/auth.json` — never in the image, never in a VM.
- The key itself is injected **per sandbox** by the sbx host proxy at create
  time, as a placeholder env var whose value is a proxy token, not the real
  credential. The VM's environment shows a value with the `sbx-cs-` prefix; the
  host substitutes the real key only on requests to allowlisted hosts.
  Both the build and the sanity probe check nothing more than that prefix:

  ```sh
  sbx exec pi-worker-sanity printenv OPENCODE_GO_API_KEY | head -c 8   # sbx-cs-
  ```

  Never print, log, or copy the full value — treat it as a credential even
  though it is only a proxy handle.
- Same mechanism for every other proxied credential in the VM (`GH_TOKEN`,
  `CONTEXT7_API_KEY`, …): placeholders in, real secrets never on disk.

The one thing the model file does carry is the **provider catalog and default
model**, which is why the template is host-independent: any host that can reach
`opencode.ai` and inject the proxy sentinel gets a working agent.

## Egress: build phase vs. runtime vs. per-sandbox policy

There are three distinct network stories, and they must not be confused. The
descriptor declares the first two in one `com.docker.sandbox/network-policy@1`
capability, split by phase:

```yaml
capabilities:
  - type: com.docker.sandbox/network-policy@1
    config:
      install:   # only the image build: npm, the Go tarball, its redirect + bucket
        allow: [registry.npmjs.org:443, go.dev:443, dl.google.com:443, storage.googleapis.com:443]
      runtime:   # the v2 allow list, carried over verbatim (8 hosts)
        allow: [registry.npmjs.org:443, opencode.ai:443, "*.opencode.ai:443", go.dev:443,
                dl.google.com:443, storage.googleapis.com:443, proxy.golang.org:443,
                sum.golang.org:443]
```

1. **Build phase** (`install.allow`) — `docker buildx` and the image build run
   under this list only. An entry in `runtime` does **not** grant access during
   a build, which is why `proxy.golang.org`/`sum.golang.org`/`opencode.ai` are
   absent there: nothing in the recipe fetches from them.
2. **Sandbox created with the kit** (the build sandbox in `build.sh`, and the
   delegate extension's fallback path) — `runtime.allow` applies to the running
   sandbox. That list is wide on purpose: it is the v2 list, unchanged, so a
   kit-created VM can do everything the v2 one could.
3. **Sandbox created from the template** (`sbx create -t pi-worker:v1 …` — the
   normal path). The template carries filesystem, not policy, so it inherits
   *nothing* from the kit's allow list. The delegate extension applies a
   **per-sandbox, default-deny** policy right after create:

   ```sh
   sbx policy allow network --sandbox <name> \
       opencode.ai:443,proxy.golang.org:443,sum.golang.org:443,storage.googleapis.com:443
   ```

   Those four are the `worker`/`general` row of the profile table above: model
   calls (`opencode.ai`), module and checksum fetches (`proxy.golang.org`,
   `sum.golang.org`), and Go tooling artefacts (`storage.googleapis.com`). The
   other roles get narrower or different lists from that same table (scout and
   librarian: model provider only; researcher: model provider, the host codex
   relay `localhost:8787`, and `context7.com`) — the table is authoritative,
   this snippet is one row. If the policy fails to apply, the extension tears
   the VM down and fails loudly — a VM without its declared egress is useless, a
   VM with undeclared egress is dangerous.

The reason for the split: baking the runtime list into every task VM would grant
each of them npm and toolchain-download reach they never need. Per-sandbox
policy is where the narrow, role-specific answer belongs.

## Known gaps / follow-ups

- **`sbx kit inspect` is advisory here.** Step 1 tolerates a CLI that refuses a
  v3 source directory and lets the `docker buildx` step be the gate. If a later
  `sbx` makes `inspect` strict about v3, the step can be promoted to fatal.
- **Workload capabilities are declared, not exercised at build time.** The
  `com.docker.sandbox/sbx@1` contract (which supplies `WORKSPACE_DIR`,
  `SANDBOX_ID`, `IS_SANDBOX`) only takes effect when a sandbox is created from
  the kit; the recipe build cannot prove it. The cold-boot probe in step 7 is
  the proof for the template path.
- **`files/` config is image content now**, so a worker's edits to
  `~/.pi/agent/*.json` persist for that sandbox's lifetime (see "Behavior
  deltas", item 2). Nothing resets them per boot any more; a future need for
  reset semantics belongs in a lifecycle `files` capability with
  `overwrite: true`, not in `COPY`.
- **Orphaned intermediate images**: a rebuild may leave the previous assembled
  image behind. Harmless, but prune occasionally (`sbx template rm <IMAGE-ID>`,
  keeping the one `pi-worker:v1` points at).

Resolved by this change (kept as the record so they are not reintroduced):

- **The delegate fallback used the wrong path and the wrong flag.** It probed
  `.pi/sandbox/pi-worker-kit` (which never existed) and passed the kit with
  `--kit`, a mixin-only flag. The kit lives at `.pi/sandboxes/kit` and is now
  passed as the **first positional** (`sbx create AGENT|SANDBOX_KIT [PATH...]`),
  with the workspaces after it.
- **The template reference was bare.** `-t pi-worker` does not resolve to
  `:v1` (verified: `image "pi-worker" is not cached locally and
  pull_policy=never`); the extension now uses
  `docker.io/library/pi-worker:v1`, which is the form `sbx template ls` prints.

## Evidence

Every run writes its full log to `dist/logs/build-<run>.log`, per-step logs to
`dist/logs/step-*.log`, and the timing table to `dist/timings.tsv` +
`dist/timings-<run>.md` (all of `dist/` is gitignored — the artifacts are
per-host). The last verified build (all steps green, model call OK, cold-boot
sanity from the template) is summarised on that host in
`dist/logs/sandbox-kit-EVIDENCE.md`.
