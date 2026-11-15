# Refactor — modular-monolith restructure (Schedule & Game + shared kernel + identity + cmd)

Status: approved by the user in the main session (decision ledger below).
Executor: background orchestrator, HANDOFF=confirm.
Recon baseline: **HEAD 921c98e**. All file:line references in the phase files come from that recon — run a scout drift-check before starting, and before any phase if commits landed on the base branch meanwhile.

Rulebook: `docs/designs/go-modular-monolith-example.md` (§1 layout, §2 core principles, §4 catalog = layered, §5 orders = hexagonal, §6 wiring). Each phase file says which sections its workers must read — workers are cold starts: inline the reading instructions and every needed specific into their task text. `tactical-design-decision-tree.png` is an architecture-selection tree; it justified the per-module style choices below and nothing more.

## Decision ledger (user-approved, do not relitigate)
1. Schedule = **layered** (catalog-style: `internal/{model,repository,service,handler}`). Game = **hexagonal** (orders-style: `internal/core/{domain,port,service}` + `internal/adapter/{inbound,outbound}`). No event-sourcing.
2. Layout: `internal/shared/**` (platform) + `internal/<module>/**` (bounded contexts: schedule, game, identity, ledger, idempotency). `internal/arch` stays top-level. Facade = `internal/<module>/<module>.go`; module internals under `internal/<module>/internal/**` (Go's nested-`internal` rule enforces privacy at compile time).
3. Shared HTTP kernel: `internal/shared/httpx` (encoding, params, middleware, ratelimiter, generic error writers) + `internal/shared/server` (process lifecycle: Serve/shutdown). Handlers + routes live **inside each module**; modules expose `RegisterRoutes`.
4. `internal/store` → `internal/shared/persistence` (Querier, Filters/Metadata, pg-error classifiers). Shared sentinel errors (`ErrRecordNotFound` etc.) **die**: each module's domain owns its sentinels, repositories translate. The game-facing port error contract flips in phase 4 together with game.
5. **Full ACL** for game→schedule: game core owns its own types (`Phase`, `MatchSnapshot`, …); `adapter/outbound/schedule_acl.go` translates from schedule's public surface. When done, `game/internal/core/domain` must not import schedule at all.
6. Identity gets the full layered split; ledger + idempotency get light alignment (rename + facade only, no layout change).
7. **BEHAVIOR FREEZE on the HTTP wire**: same 47 routes, methods, status codes, JSON bodies. DTOs copy existing json tags **verbatim** — including the odd `json:"_"` on `schedule.Match.EndedAt` and its use as a request field in the resolve handler, and the hardcoded ledger stub (TODO) in the ride-create flow. Fixes are follow-ups, not this refactor.
8. Verification with local `LT_API_TEST_DSN` (full DB suite, same as CI's `make audit`).
9. Vault Phase 05 note + README updated at the end (librarian); historical `docs/league-tokens-learning-phases/**` untouched.
10. HANDOFF=confirm.

## Invariants — must hold at EVERY commit
- `go build ./...`, `go vet ./...`, `gofmt -l` (empty), `go test ./...` all pass. DB-backed suites run when `LT_API_TEST_DSN` is available in the job env; if missing, source the parent checkout's `../lt-api/.envrc` for verification runs **only** — never copy or commit it. AGENTS.md carries the sandbox/DSN facts; workers read it automatically.
- HTTP wire contract frozen (ledger 7). Final route set must be identical to baseline `internal/api/routes.go`.
- DB schema untouched: no edits to `migrations/**`, no new migrations, `cmd/seed/**` untouched.
- Boundaries (enforced by `internal/arch`, made path-aware in phase 2):
  - `internal/shared/**` never imports `internal/{schedule,game,identity,ledger,idempotency}` or their subpackages.
  - Module code imports: its own `internal/**`, `internal/shared/**`, and other modules' **root** package only (never `internal/<other>/internal/...`).
  - `cmd/**` is the composition root and may import anything (as today).
- Domain purity: schedule `model` + game `core/domain` import only stdlib + `internal/shared/validator` + `shopspring/decimal` (game only, value-type lib). NO pgx, NO persistence, NO eventbus, NO other module.
- Naming: package = folder, lowercase; files `<entity>_<role>.go` — model uses the bare entity name; `_repo.go` (never `_repository.go`), `_service.go`, `_handler.go`; facade `<module>.go`; constructors `New*`; interfaces consumer-side; domain sentinels in the domain/model package; **no `Store` type/identifier survives** (grep guard in phase 6).
- Event-publishing semantics preserved when publish moves from repository to service: same post-commit timing, same payload, same ordering.
- Git: stage exactly the phase's files (never `git add -A`); one commit per coherent step, `refactor(N): ...` conventional style; never commit `.envrc`, `bin/`, `.DS_Store`, `scratch/`, `.pi/jobs/`.
- No new third-party dependencies; `go.mod` module path unchanged; httprouter stays the router (do NOT migrate to stdlib mux patterns).
- If a phase reveals a plan mapping is impossible without behavior change: stop that line of work, pick the minimal behavior-preserving alternative, record the deviation in the commit message and the final report. Never silently change wire behavior.

## Target layout

```
internal/
├── arch/                          # boundary test (path-aware from phase 2; final matrix in phase 6)
├── shared/
│   ├── httpx/                     # from internal/api: encoding, params, middleware, context, ratelimiter, generic error writers
│   ├── server/                    # from internal/api/server.go: Server, RunOptions, Serve, shutdown + E2E integration suite (phase 6)
│   ├── persistence/               # from internal/store: Querier, Filters/Metadata, pg classifiers (sentinels removed phase 6)
│   ├── eventbus/  validator/  logging/  mailer/  config/  constants/  vcs/  testdb/
├── schedule/                      # layered module
│   ├── schedule.go                # facade: Module, NewModule, DTOs, capability ifaces, RegisterRoutes
│   └── internal/{model,repository,service,handler}/
├── game/                          # hexagonal module
│   ├── game.go                    # facade: Module, NewModule, DTOs, capability ifaces, RegisterRoutes
│   └── internal/core/{domain,port,service}/
│       + internal/adapter/inbound/http/
│       + internal/adapter/outbound/{postgres/, schedule_acl.go}
├── identity/                      # layered module (same shape as schedule)
│   ├── identity.go                # facade incl. UserForToken → satisfies httpx.UserLookup
│   └── internal/{model,repository,service,handler}/
├── ledger/                        # light: ledger.go (iface + facade), postgres.go (Repository)
└── idempotency/                   # light: facade, IdempotencyStore → Repository
cmd/
├── api/                           # from cmd/server — thin, explicit composition root
└── seed/                          # untouched
```

## Phase index

| # | File | Scope | Depends on |
|---|---|---|---|
| 1 | phase-01-cmd-api.md | cmd/server → cmd/api + references | – |
| 2 | phase-02-shared-kernel.md | shared/httpx + shared/server carved from internal/api; store → shared/persistence; arch path-aware | 1 |
| 3 | phase-03-schedule.md | schedule module (layered), handlers migrate | 2 |
| 4 | phase-04-game.md | game module (hexagonal) + ACL; ports for ledger/idempotency | 3 |
| 5 | phase-05-identity.md | identity module (layered); ledger + idempotency light renames | 2 |
| 6 | phase-06-converge.md | delete internal/api; E2E tests → shared/server; sentinels out; arch final matrix; cmd/api final | 3, 4, 5 |
| 7 | phase-07-verify-document.md | full audit, route diff, grep sweeps, README, vault (librarian) | 6 |

**Hot-file contention (phases 3–5):** the module trees are disjoint per phase and safe for parallel workers. But `internal/api/routes.go`, `internal/api/errors.go`, `cmd/api/main.go`, `internal/arch/arch_test.go` are touched by all three. Either serialize 3→4→5, or let module workers deliver packages + `RegisterRoutes` without touching hot files and apply each wiring commit yourself between worker rounds. Recommended: 3 and 5 in parallel (orchestrator applies wiring commits), then 4.

## Verification protocol
Per phase: the phase file's "Done when" + the invariants. Final (phase 7): full `make audit` with `LT_API_TEST_DSN`; route-table diff vs baseline (`git show 921c98e:internal/api/routes.go` vs final registrations — same 47 routes, same methods+paths+middleware); grep sweeps (phase 7); Test-function count must not decrease (each phase file lists where its tests move).

## Handoff
HANDOFF=confirm. After phase 7, emit AWAITING_APPROVAL containing: branch + worktree path; commit list (phase → sha → subject); files-changed stats; verification output (audit incl. DB-suite status and whether `LT_API_TEST_DSN` was available); route-diff result; grep-sweep results; every deviation from this plan with justification. Then stop.

## Out of scope (do not do)
- Dissolving `internal/constants` into owning modules (follow-up, noted)
- Fixing the `json:"_"` EndedAt oddity or the hardcoded ledger token stub (frozen; keep the TODO)
- Event-sourcing/CQRS; leaderboard/balance module moves (vault Phase 05 tasks 2 & 4)
- Editing `docs/league-tokens-learning-phases/**`, `.pi/plans/delegate/**`, `scratch/**`, `migrations/**`, `cmd/seed/**`
- Router replacement, module-path rename, new dependencies
