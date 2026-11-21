# pi-worker sandbox kit → `pi-worker:v1` template

This directory builds the **sbx image that delegate worker VMs boot from**: a Go
toolchain, the in-VM `pi` coding agent, its provider/model config, and a warm Go
module cache — all baked into one snapshot so a task sandbox starts ready to
build instead of ready to install.

It builds *only* the machine. Everything that is per-task or sensitive — the
worktree mount, egress rules, the model key — is attached when a sandbox is
*created*, never captured in the snapshot (see "Secrets" and "Egress" below).

```
.pi/sandboxes/
├── kit/spec.yaml     the kit (kit-spec v2: image, env, network allow, install hooks, files)
├── build.sh          the build pipeline (validate → pack → create → warm → snapshot → prove → clean)
├── README.md         this file
└── dist/             artifacts, gitignored: pi-worker.zip, timings.tsv, timings-*.md, logs/
```

## What the snapshot contains

| item | detail |
|---|---|
| base image | `docker/sandbox-templates:shell-docker` (agent UID 1000, Node/npm, sandbox proxy plumbing) |
| Go | `1.27.1` at `/usr/local/go`, symlinked to `/usr/local/bin/go` and `gofmt` |
| pi | `@earendil-works/pi-coding-agent@1.5.2`, global npm install |
| pi config | `/home/agent/.pi/agent/models.json` (opencode-go provider catalog) + `settings.json` (default `opencode-go` / `glm-5.3` / thinking `high`) |
| Go module cache | `/home/agent/go/pkg/mod`, ~132 MB / 45 module zips (this repo's `go.mod` graph) |
| template tag | `pi-worker:v1` |

Nothing auto-runs on boot: the kit sets no entrypoint, so the delegate extension
starts `pi` itself through `sbx exec`.

## Rebuild

```sh
./.pi/sandboxes/build.sh          # ~3 min on an M-series host, from scratch
```

Run it from anywhere; paths resolve from the script. End state is always
`sandbox ls` empty and `template ls` showing `pi-worker:v1`.

| env | default | meaning |
|---|---|---|
| `KIT_SOURCE` | `zip` | compose the build sandbox from `dist/pi-worker.zip` (`zip`) or straight from `kit/` (`dir`). Source-form builds go through a shared `sbx-kit-builder` cache sandbox, which the script removes at the end. |
| `KEEP` | `0` | `1` keeps `pi-worker-build` and `pi-worker-sanity` around for inspection. |
| `MODEL_CALL` | `1` | `0` skips the in-VM live model call (the only step that costs tokens). |
| `SKIP_REBUILD` | `0` | `1` exits early if `pi-worker:v1` already exists. |

Pipeline (each step's wall-clock is recorded in `dist/timings.tsv` and
`dist/timings-<run>.md`):

0. **pre-clean** stale build/sanity sandboxes. The old `pi-worker:v1` tag is
   left alone until snapshot time, so a failed run never destroys a usable
   template.
1. **validate + pack** the kit (`sbx kit validate`, `sbx kit pack`, then an
   offline validate of the packed zip) into `dist/pi-worker.zip`.
2. **create** the build sandbox from the kit — this is where Go, npm and the
   config files get installed.
3. **in-VM checks** — `go version`, `pi --version`, worktree mounted at the same
   absolute path, `OPENCODE_GO_API_KEY` placeholder prefix.
4. **warm `GOMODCACHE`** — `go mod download all` in the worktree.
5. **live model call** — `pi -p --model opencode-go/glm-5.3 "reply OK"` through
   the host proxy. Cheap, and the only end-to-end proof that sentinel key +
   `models.json` + egress line up. A failure is logged, not fatal.
6. **stop + `sbx template save`** → `pi-worker:v1` (the filesystem snapshot,
   module cache included).
7. **fresh create from the template** — a cold-boot sandbox is created from
   `pi-worker:v1` and re-probed: `go version`, `pi --version`, key prefix, baked
   pi config, and a non-empty `GOMODCACHE` (an empty cache fails the run: the
   template would be pointless).
8. **teardown** — both sandboxes removed.
9. **deliverables** — timings written, template and sandbox lists printed.

Long, quiet steps (sandbox create, module warm, model call, snapshot, create
from template) run **detached**: output goes to `dist/logs/step-*-<run>.log`,
the script polls it and prints a heartbeat, and a per-step budget bounds the
wait. That keeps the run from ever sitting silent for minutes — a silent pipe is
what got an earlier run killed by a stall watchdog, and the same silence also
delays the visible output of a short step, which is why the timing helper never
lets its timeout watcher inherit the log pipe.

`SKIP_REBUILD=1` is a ~1 s no-op when the template exists; `bash -n` is enough
to check an edit, but the script is normally validated by running it, since the
whole point is the live proof in step 7.

### Version pins

Pins live in exactly two places, and the build asserts both:

- **Go `1.27.1`** — the `go` directive in the repo root `go.mod`. The kit's
  install hook hardcodes the same version (`GO_VERSION` in
  `kit/spec.yaml`); bump both together. The host `go version` is irrelevant: the
  toolchain always comes from `go.dev/dl`, arch-detected (`arm64`/`amd64`).
- **pi `1.5.2`** — pinned in the kit's npm install. The build prints the host's
  `pi --version` as a cross-check, so host/VM drift is visible in the log.

The base image is pinned by local tag (`--pull never`), and the resulting
template by `pi-worker:v1`. Rebuilding replaces the tag with the freshly built
image. Each rebuild also leaves the previous `sandbox-kit-assembled:*` image
behind (housekeeping: `sbx template ls`, then `sbx template rm <IMAGE-ID>` for
orphans — the tag form 404s; never remove the image ID `pi-worker:v1` points
at).

## Why the Go module cache is warmed

`sbx template save` snapshots the **sandbox filesystem**, so anything written
into the agent's home is inside the template. The kit therefore deliberately
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

The kit sets `GOPROXY=https://proxy.golang.org` — no `,direct`. `,direct` is the
escape hatch that makes the go command fall back to **direct VCS fetching**
(git/ssh to arbitrary hosts) when the proxy fails. That would be:

- a bypass route around the egress allowlist — the whole point of the list is
  that module bytes only ever come from `proxy.golang.org` / `sum.golang.org`;
- an extra guaranteed failure (egress denies those hosts) that buries the real
  error under retries.

Proxy-only keeps the network contract exact: if the proxy is down, module
fetches fail loudly and immediately. `GOSUMDB=sum.golang.org` and
`GOTOOLCHAIN=local` complete the same story — no unexpected toolchain downloads,
only one verified checksum database.

**Caveat, measured — kit env vars are per-sandbox, not image-level.** `sbx`
applies the kit's `environment.variables` to sandboxes created *with the kit*; a
filesystem snapshot cannot carry environment. A sandbox created from
`pi-worker:v1` alone therefore reports Go's own defaults:

```
$ sbx exec pi-worker-sanity go env GOPROXY GOTOOLCHAIN
https://proxy.golang.org,direct
auto
```

That is currently harmless — `go.mod` pins go 1.27.1, which *is* the installed
toolchain (so `auto` never downloads anything), `GOMODCACHE` still resolves to
the warmed path, and egress is default-deny, so `,direct` cannot actually leave
the sandbox. It is still a gap between intent and reality: if a future kit
version wants the strict values baked in, add
`go env -w GOPROXY=https://proxy.golang.org GOTOOLCHAIN=local` to the install
hook — that writes `/home/agent/.config/go/env`, which *is* inside the snapshot.
`PI_AGENT_HOME` is likewise absent in template-only sandboxes; pi's default
(`~/.pi/agent`) is the same path, so it does not matter in practice.

## Secrets: the template contains none

The snapshot is a long-lived, shared artifact, so no credential may enter it.

- `models.json` stores `"apiKey": "$OPENCODE_GO_API_KEY"` — a *sentinel*, not a
  key.
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

## Egress: kit list vs. per-sandbox policy

There are two distinct network stories, and they must not be confused:

1. **Sandbox created *with the kit*** (the build sandbox, and the fallback path
   in the delegate extension). The kit's `permissions.network.allow` list —
   `registry.npmjs.org`, `opencode.ai`, `*.opencode.ai`, `go.dev`,
   `dl.google.com`, `storage.googleapis.com`, `proxy.golang.org`,
   `sum.golang.org` — governs both the install phase and runtime, and it
   **persists on that sandbox** as long as it lives. That list is wide because
   the install phase needs npm, the Go tarball host, and friends.
2. **Sandbox created from the template** (`sbx create -t pi-worker:v1 …` — the
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

The reason for the split: baking the install-time list into every task VM would
grant each of them npm and toolchain-download reach they never need. Per-sandbox
policy is where the narrow, role-specific answer belongs.

## Known gaps / follow-ups

- **Template name vs. tag.** This build produces `pi-worker:v1`. The delegate
  extension's `SANDBOX_TEMPLATE_NAME` is `pi-worker`, and an untagged `-t
  pi-worker` does **not** resolve to `:v1` (verified: `image "pi-worker" is not
  cached locally and pull_policy=never`). Its kit fallback path
  (`.pi/sandbox/pi-worker-kit`) does not exist either — the kit lives at
  `.pi/sandboxes/kit`. Both are outside this directory; they need to become
  `pi-worker:v1` and `.pi/sandboxes/kit` (or the zip) before created-from-
  template VMs work end to end.
- **Kit env vars are not baked** into the template (see the caveat above).
- **Orphaned intermediate images**: each rebuild leaves the previous
  `sandbox-kit-assembled:*` image behind. Harmless, but prune occasionally
  (`sbx template rm <IMAGE-ID>`, keeping the one `pi-worker:v1` points at).

## Evidence

Every run writes its full log to `dist/logs/build-<run>.log`, per-step logs to
`dist/logs/step-*.log`, and the timing table to `dist/timings.tsv` +
`dist/timings-<run>.md` (all of `dist/` is gitignored — the artifacts are
per-host). The verified build (all steps green, model call OK, cold-boot sanity
from the template) is summarised on that host in
`dist/logs/sandbox-kit-EVIDENCE.md`.
