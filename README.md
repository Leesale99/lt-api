# lt-api

**League Tokens API** — a backend service for a game token system, built as a progressive learning project. The goal is not to ship fast, but to build deep understanding of backend fundamentals one phase at a time.

## Tech stack

- **Go** (1.27)
- `net/http` standard library (routing/mux)
- `log/slog` for structured logging
- [`julienschmidt/httprouter`](https://github.com/julienschmidt/httprouter)

## Learning path

Implementation follows the phased plan in [`docs/league-tokens-learning-phases/`](docs/league-tokens-learning-phases/README.md):

0. HTTP/API foundation
1. PostgreSQL persistence
2. Domain model & state machines
3. Transactions & atomic operations
4. Concurrency & idempotency
5. Modular monolith boundaries
6. Ledger & accounting authority
7. Background workers
8. Transactional outbox & events

Each phase introduces one major backend concept only when the system has a concrete reason to need it. Things deliberately deferred: microservices, Kubernetes, message brokers, Redis, gRPC, and heavy architectural frameworks.

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
                      outbound/{postgres,scheduleacl}}.
internal/identity/    Layered module: facade identity.go + internal/{model,
                      repository, service, handler}.
internal/ledger/      Thin module: ledger.go + postgres.go.
docs/                 Learning-oriented implementation plan (phases 0–8)
```

Bounded contexts sit behind facades: the module root is the only package
outsiders may import, and everything else lives under
`internal/<ctx>/internal/` so the layers cannot leak — the privacy is
compile-time, not convention. `httpx`, `persistence`, `eventbus`, `validator`
and `idempotency` are the kernel, nested under `internal/kernel/`; `config`,
`logging`, `mailer` and `server` are the platform, nested under
`internal/platform/` (the VCS version helper is inlined into `config`). They
classify as `kernel:<name>` and `platform:<name>` in the boundary matrix.
Only the test harness (`internal/testdb`) and the matrix itself
(`internal/arch`) sit outside those classes, as `shared:<name>`.

Each module owns its own route table (`RegisterRoutes`), and `cmd/api`
composes them — so the wire surface is identical to the pre-refactor one.
The E2E suite lives in `internal/e2e` and composes the
real modules exactly like `cmd/api/main.go` through one shared helper.

## Database: context schemas, per-context roles and configuration

The Postgres layout mirrors the bounded contexts (ADR-028), and the
boundary is enforced by Postgres itself (migrations 000015–000018):

- `schedule` — teams, seasons, rounds, matches (+ their internal gates)
- `game` — rides, players, idempotency_keys
- `identity` — users, user_tokens, roles, permissions, roles_permissions

One NOLOGIN role per context — `schedule_app`, `game_app`, `identity_app` —
holds exactly its own schema's CRUD; `game_app` can additionally EXECUTE
schedule's two SECURITY DEFINER round guards (the write-instant gates ride
commands need inside their own transaction) and nothing else on schedule.
The server opens one pool per context and runs `SET ROLE <context_role>` +
`SET search_path` on every connection, so a cross-context query fails on
permission even when the name is resolvable.

### Configuration surface

| Variable | Meaning |
| --- | --- |
| `LT_API_DSN` | Base DSN; all three context pools derive from it |
| `LT_API_DSN_SCHEDULE` | Optional override: schedule pool only |
| `LT_API_DSN_GAME` | Optional override: game pool only |
| `LT_API_DSN_IDENTITY` | Optional override: identity pool only |
| `LT_API_DB_MAX_CONNS` | Max connections PER context pool (the budget is per pool) |

The per-context DSN overrides are for deployments that split the contexts
onto separate databases or login roles; each override pool still runs the
same `SET ROLE` hook, so the login role it connects as must hold that
context's membership.

### Role requirements for the migration runner

`migrate` (and `cmd/seed`, which runs as the same owner connection) must
run as a role with `CREATEDB` **and** `CREATEROLE`: migration 000018
creates the context roles, `ALTER ROLE`s their search_path and grants them
membership to the runner. Roles are cluster-global, so every database that
runs this migration chain should be migrated by the SAME runner role; if a
second runner role needs the chain (e.g. a test role while the dev role
created the roles), an admin hands over membership + admin once:

```sql
GRANT schedule_app, game_app, identity_app TO lt_test WITH ADMIN OPTION;
```

After that the second role can run the migration against its own databases
(it owns the tables it creates there, so the per-schema GRANTs always work;
the membership grant makes `SET ROLE` possible).

### Bounded contexts: who owns what (Phase 05, as built)

The schema split above mirrors the module split. **Schedule** owns teams,
seasons, rounds and matches, mints their ids, and is the ACL for the upstream
feed; it publishes `MatchResultRecorded` and `RoundCompleted` on the
in-process eventbus (transactional outbox deferred to Phase 08). **Game**
owns rides and players, drives the ride state transitions and settlement, and
reaches schedule only through the `SchedulePort` and ledger only through the
extracted `Ledger` interface (`LockTokens` / `Burn` money intents) — never
through schedule's or ledger's tables. **Ledger** owns balances and is thin:
money intents in, it writes its own tables. **Identity** is the generic
supporting context. `cmd/api` is the composition root, not a context: it owns
the router, the middleware chain and the healthcheck, and the only HTTP
surface above the modules is the shared `httpx` transport.

The import edges between these packages are pinned in code by
`internal/arch/arch_test.go`; the data-flow view lives in
`docs/designs/bounded-contexts-dependency-map.mmd`.
