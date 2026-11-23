# lt-api

**League Tokens API** — a Go backend service for a game token system, built as a progressive learning project:
the goal is to build deep understanding of backend fundamentals one phase at a time, not to ship fast. The
service is one modular monolith; three support systems sit around it and each owns one job. The executable
specification in `docs/spec/` states which business rules the system holds, who decides each one, and how we
know it still holds. The delegated-work harness in `.pi/`, with its machine-checked policy in `WORKFLOW.md`,
decides who may change what and which gate certifies the change. The learning plan in
`docs/league-tokens-learning-phases/` explains each backend concept in the order this project introduces it.
The developer hub in `docs/README.md` is the map across all of them.

Start with the row that matches your question:

| Question | Read |
| --- | --- |
| Where does a new developer start? | [`docs/README.md`](docs/README.md) — the four planes, the one-change flow, where each gate lives |
| Which business rules does the system hold, and what enforces them? | [`docs/spec/README.md`](docs/spec/README.md) — the rule catalog, the Gherkin scenarios, the checker |
| Who may change what, and what proves a change is good? | [`docs/delegate-workflow.md`](docs/delegate-workflow.md) for the narrative, [`WORKFLOW.md`](WORKFLOW.md) for the policy |
| Why does a concept exist, and in which phase do we add it? | [`docs/league-tokens-learning-phases/README.md`](docs/league-tokens-learning-phases/README.md) |
| Why was a decision made, and what did we learn? | [`.pi/skills/lt-api-kb/SKILL.md`](.pi/skills/lt-api-kb/SKILL.md) — the knowledge vault |

## Tech stack

The runtime surface stays deliberately small, so each phase introduces machinery only when the system needs
it:

- **Go** (1.27)
- `net/http` standard library (routing/mux)
- `log/slog` for structured logging
- [`julienschmidt/httprouter`](https://github.com/julienschmidt/httprouter)

## Learning path

Implementation follows the phased plan in
[`docs/league-tokens-learning-phases/`](docs/league-tokens-learning-phases/README.md), with one guide file per
phase:

0. HTTP/API foundation
1. PostgreSQL persistence
2. Domain model & state machines
3. Transactions & atomic operations
4. Concurrency & idempotency
5. Modular monolith boundaries
6. Ledger & accounting authority
7. Background workers
8. Transactional outbox & events

Each phase introduces one major backend concept only when the system has a concrete reason to need it. Things
deliberately deferred: microservices, Kubernetes, message brokers, Redis, gRPC, and heavy architectural
frameworks.

Two domain documents in `docs/league-tokens-learning-phases/` are the source of truth the phases build on:
`domain-knowledge.md` is the player-facing game design, and `domain-knowledge-game-engine.md` defines season
and match lifecycles, Ride behavior, and the engine states the system must protect.

## Project layout

```
cmd/api/              Entry point + composition root, in explicit order:
                      config → logging → mailer → per-context pools →
                      eventbus → schedule.NewModule → game.NewModule (ACL
                      wired in) → identity.NewModule → eventbus wiring →
                      httpx middleware chain → router → platform server →
                      Serve. Side files: routes.go (router, 404/405),
                      healthcheck.go, gamerefs.go (schedule↔game seam
                      adapter: the only place both vocabularies meet).
internal/arch/        Boundary matrix, path-aware (kernel:*/platform:*/
                      shared:* classes, module classes collapse nested
                      packages, cmd row): TestImportBoundaries +
                      TestKernelImportsOnlyKernel +
                      TestNonModulesNeverImportModules +
                      TestModulesNeverImportPlatform.
internal/kernel/      Kernel machinery: httpx (transport: JSON envelope/codecs,
                      params, filters, error writers, request context,
                      middleware incl. ratelimiter), persistence (Querier,
                      Filters/Metadata, pg-error classifiers — no shared
                      sentinels), eventbus, validator, idempotency (the
                      claim-first single-flight engine — the first stateful
                      kernel package, it owns idempotency_keys). All of it
                      classifies as kernel:<name> in the boundary matrix.
internal/platform/    Process-level technical packages, classed
                      platform:<name>: config (variable names, precedence,
                      semantic validation, inlined VCS version), logging (the
                      process-wide slog logger + pgx bridge), mailer (SMTP
                      delivery, embedded templates), server (process
                      lifecycle: Serve/shutdown/RunOptions/background tasks).
internal/e2e/         Acceptance suite: the composition root in test form,
                      composing the real modules over the real test pools.
internal/testdb/      DB test harness (shared:testdb in the matrix).
internal/schedule/    Layered module: facade schedule.go + internal/{model,
                      repository, service, handler}.
internal/game/        Hexagonal module: facade game.go + internal/core/{domain,
                      port, service} + internal/adapter/{inbound/http,
                      outbound/{postgres,schedule_acl}}.
internal/identity/    Layered module: facade identity.go + internal/{model,
                      repository, service, handler}.
internal/ledger/      Thin module: ledger.go + postgres.go.
docs/                 Developer hub (docs/README.md: the four planes, the
                      one-change flow, where each gate lives), the executable
                      specification (docs/spec/) and the learning-oriented
                      implementation plan (phases 0–8)
```

Bounded contexts sit behind facades: the module root is the only package outsiders may import, and everything
else lives under `internal/<ctx>/internal/` so the layers cannot leak — the privacy is compile-time, not
convention. Every package classifies into one of three boundary-matrix classes:

- `kernel:<name>` — `httpx`, `persistence`, `eventbus`, `validator` and `idempotency`, nested under
  `internal/kernel/`.
- `platform:<name>` — `config`, `logging`, `mailer` and `server`, nested under `internal/platform/` (the VCS
  version helper is inlined into `config`).
- `shared:<name>` — only the test harness (`internal/testdb`) and the matrix itself (`internal/arch`).

Each module owns its own route table (`RegisterRoutes`), and `cmd/api` composes them, so the wire surface is
declared module by module and assembled in the composition root. The E2E suite lives in `internal/e2e` and
composes the real modules exactly like `cmd/api/main.go` through one shared helper.

Two files state these rules in code and in picture form: `internal/arch/arch_test.go` pins every import edge
between the module, kernel, platform and shared classes, so a new import either fits the matrix or fails the
build, and `docs/designs/bounded-contexts-dependency-map.mmd` draws the data-flow view of the same contexts.

## Database: context schemas, per-context roles and configuration

The Postgres layout mirrors the bounded contexts — one schema per context — and the boundary is enforced by
Postgres itself (migrations 000015 through 000018). The decision is recorded as ADR-028 in the vault at
`~/Projects/vaults/lt-api`:

- `schedule` — teams, seasons, rounds, matches (+ their internal gates)
- `game` — rides, players, idempotency_keys
- `identity` — users, user_tokens, roles, permissions, roles_permissions

Four migrations carry that split:

- 000015 gives each context its own schema
- 000016 drops the cross-context foreign keys the split makes unenforceable
- 000017 moves the ride-result agreement check out of a database trigger into game's Go code
- 000018 creates the per-context roles with their grants

One NOLOGIN role per context — `schedule_app`, `game_app`, `identity_app` — holds exactly its own schema's
CRUD; `game_app` can additionally EXECUTE schedule's two SECURITY DEFINER round guards (the write-instant
gates ride commands need inside their own transaction) and nothing else on schedule. The server opens one pool
per context and runs `SET ROLE <context_role>` + `SET search_path` on every connection, so a cross-context
query fails on permission even when the name is resolvable.

### Configuration surface

| Variable | Meaning |
| --- | --- |
| `LT_API_DSN` | Base DSN; all three context pools derive from it |
| `LT_API_DSN_SCHEDULE` | Optional override: schedule pool only |
| `LT_API_DSN_GAME` | Optional override: game pool only |
| `LT_API_DSN_IDENTITY` | Optional override: identity pool only |
| `LT_API_DB_MAX_CONNS` | Max connections PER context pool (the budget is per pool) |

The per-context DSN overrides are for deployments that split the contexts onto separate databases or login
roles; each override pool still runs the same `SET ROLE` hook, so the login role it connects as must hold that
context's membership.

### Role requirements for the migration runner

`migrate` (and `cmd/seed`, which runs as the same owner connection) must run as a role with `CREATEDB` **and**
`CREATEROLE`: migration 000018 creates the context roles, `ALTER ROLE`s their search_path and grants them
membership to the runner. Roles are cluster-global, so every database that runs this migration chain should be
migrated by the SAME runner role; if a second runner role needs the chain (e.g. a test role while the dev role
created the roles), an admin hands over membership + admin once:

```sql
GRANT schedule_app, game_app, identity_app TO lt_test WITH ADMIN OPTION;
```

After that the second role can run the migration against its own databases (it owns the tables it creates
there, so the per-schema GRANTs always work; the membership grant makes `SET ROLE` possible).

### Bounded contexts: who owns what

The three-schema split mirrors the module split. Each context owns one slice of the domain:

- **Schedule** owns teams, seasons, rounds and matches, mints their ids, and is the ACL for the upstream feed;
  it publishes `MatchResultRecorded` and `RoundCompleted` on the in-process eventbus (transactional outbox
  deferred to Phase 08).
- **Game** owns rides and players, drives the ride state transitions and settlement, and reaches schedule only
  through the `Schedule` port and ledger only through the extracted `Ledger` interface (`LockTokens` / `Burn`
  money intents) — never through schedule's or ledger's tables.
- **Ledger** owns balances and is thin: money intents in, it writes its own tables.
- **Identity** is the generic supporting context.

`cmd/api` is the composition root, not a context: it owns the router, the middleware chain and the
healthcheck, and the only HTTP surface above the modules is the shared `httpx` transport.

The import edges between these packages are pinned in code by `internal/arch/arch_test.go`; the data-flow view
lives in `docs/designs/bounded-contexts-dependency-map.mmd`.

## What this does not cover

This page covers the service and the repo layout; the rule catalog lives in `docs/spec/`, the delegate harness
in `docs/delegate-workflow.md` and `WORKFLOW.md`, and the learning path in
`docs/league-tokens-learning-phases/`.
