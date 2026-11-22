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

```
.pi/sandboxes/
├── kit/
│   ├── pi-worker.yaml         v3 descriptor: schemaVersion 3, kind: workload,
│   │                          dockerfile:, capabilities (sbx@1 + network-policy@1)
│   ├── pi-worker.dockerfile   the recipe: FROM shell-docker, ENV, RUN go/npm, COPY pi config
│   └── files/
│       ├── models.json        opencode-go provider catalog (COPY'd into the image)
│       └── settings.json      pi defaults (COPY'd into the image)
├── build.sh                   the build pipeline (validate → build → warm → snapshot → prove → clean)
├── README.md                  this file
└── dist/                      artifacts, gitignored: timings.tsv, timings-*.md, logs/
```

## What the snapshot contains

| item | detail |
|---|---|
| base image | `docker/sandbox-templates:shell-docker` (recipe `FROM`, agent UID 1000, Node/npm, sandbox proxy plumbing) |
| Go | `1.27.1` at `/usr/local/go`, symlinked to `/usr/local/bin/go` and `gofmt` (`ARG GO_VERSION`) |
| pi | `@earendil-works/pi-coding-agent@1.6.0`, global npm install (`ARG PI_VERSION`) |
| image env | `GOTOOLCHAIN=local`, `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `PI_AGENT_HOME=/home/agent/.pi/agent` (`ENV`) — plus the same three Go values in `/home/agent/.config/go/env` |
| pi config | `/home/agent/.pi/agent/models.json` (opencode-go provider catalog) + `settings.json` (default `opencode-go` / `glm-5.3` / thinking `high`), `COPY --chown=1000:1000` |
| Go module cache | `/home/agent/go/pkg/mod`, ~132 MB / 45 module zips (this repo's `go.mod` graph) |
| template tag | `pi-worker:v1` |

Nothing auto-runs on boot: the recipe sets no entrypoint (the base image's
shell-compatible launcher keeps the sandbox alive), so the delegate extension
starts `pi` itself through `sbx exec`.

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

   Those four are all a task needs: model calls (`opencode.ai`), module and
   checksum fetches (`proxy.golang.org`, `sum.golang.org`), and Go tooling
   artefacts (`storage.googleapis.com`). If the policy fails to apply, the
   extension tears the VM down and fails loudly — a VM without its declared
   egress is useless, a VM with undeclared egress is dangerous.

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
