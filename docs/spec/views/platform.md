<!-- GENERATED — do not edit. -->
# Business rules — `platform`

> **GENERATED — do not edit.** This is the `platform` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/platform/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**17 rules** — 15 implemented, 2 specified-only.

## Module `platform`

### Entity `configuration`

#### R-PLATFORM-001 — Configuration is resolved flag over environment over default

`implemented` · `orchestration` · authority `composition-root`

A deployment's settings come from its environment, and an operator may override them per run with a flag; when neither says anything, a built-in default applies. Values that are deployment data rather than per-run operator choices — the cross-origin allowlist, the build version — are not overridable at all.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::Parse` | `composition-root` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `composition-root` | `authoritative` | — |

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`

#### R-PLATFORM-002 — Startup refuses a configuration it cannot honour, and names the setting

`implemented` · `orchestration` · authority `composition-root`

A configuration value that is missing, unparseable or out of its allowed range stops the process before it serves anything, and the message names the setting that must change — both its environment variable and its command-line flag, because the same value can arrive either way. Every bad environment value is reported in one refusal instead of one at a time, and nothing degrades into a silent default.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::Parse` | `composition-root` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `composition-root` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`
- unit: `internal/platform/config/config_test.go::TestValidate`

#### R-PLATFORM-003 — Every setting has a bound, and the bound holds when the feature is off

`implemented` · `orchestration` · authority `composition-root`

Each setting is either usable or refused: the listen port and the mail port are real ports, the outbound sender is an address mail can be sent from, the environment is one of the three the deployment knows, a database connection string is present and parseable, the pool is at least one connection with a positive idle limit, and the throttle rates are positive. A setting is judged on its own merits — a throttle that is switched off must still be configured with usable values, because config that is only correct while something else is off fails unpredictably when it is switched on.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::validate` | `composition-root` | `authoritative` | — |
| `internal/platform/config/config.go::parseLogLevel` | `composition-root` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestValidate`
- unit: `internal/platform/config/config_test.go::TestParse`

#### R-PLATFORM-004 — A log level is an enumerated name, never a silent fallback

`implemented` · `orchestration` · authority `composition-root`

The logging level is one of the four names the deployment documents — debug, info, warn, error — matched without regard to case. An unrecognized level stops startup and the message names where the bad value came from, because a fallback would either flood or silence a deployment whose level was mistyped.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::parseLogLevel` | `composition-root` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `composition-root` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`

### Entity `context_dependency`

#### R-PLATFORM-013 — Contexts depend one way and meet only at facades

`implemented` · `boundary` · authority `composition-root`

A context uses another context only through the other's public operations, in one allowed direction, and never reaches into its tables. The context that owns the fact answers; the context that needs it declares what it needs and has it satisfied from outside; and where the two vocabularies meet, the translation happens in one place so neither context speaks the other's error language.

**Authority** `composition-root` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `cmd/api/main.go::main` | `service` | `authoritative` | — |
| `cmd/api/gamerefs.go::gameRefs.DeletePlayersForSeasonTx` | `service` | `authoritative` | — |
| `internal/identity/identity.go::Module` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::UserLookup` | `handler` | `defense-in-depth` | — |
| `internal/kernel/httpx/middleware.go::PermissionLookup` | `handler` | `defense-in-depth` | — |

**Verification**

- unit: `internal/arch/arch_test.go::TestImportBoundaries`
- unit: `internal/arch/arch_test.go::TestNonModulesNeverImportModules`
- unit: `internal/arch/arch_test.go::TestModulesNeverImportPlatform`
- unit: `internal/arch/arch_test.go::TestKernelImportsOnlyKernel`
- e2e: `internal/e2e/teams_test.go::TestDeleteTeamHandler`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

**Notes**

> Authority is the composition root (ADR-028): it is the only place that may import everything, so it is where every direction is chosen — the game context receives the calendar facade (wrapped by its own anti-corruption layer), the identity facade (as a consumer-side lookup), the ledger and the idempotency engine; the calendar receives the game-reference port from outside rather than importing game. Layer note: the wiring has no layer of its own in the frozen enum; it is recorded as service. The pinned direction itself lives in internal/arch/arch_test.go's allowedImports matrix (test-time enforcement, so it appears under verification, not as a site): a module reaches another module's root only, nothing below the modules reaches a module, no module reaches the platform, the kernel reaches the kernel only. The gamerefs adapter is the single vocabulary translator (game's record-in-use becomes the calendar's), which is what keeps the delete refusals byte-identical. Merged with provisional R-IDENTITY-021 (Phase B3): identity's own statement of this rule — other parts reach identity through its published surface only (the two lookups httpx declares and the account read the player flow needs), never its models, repositories or tables, and the transport layer holds no identity import — is the same composition-root decision seen from one edge, so it is one rule with the union of sites and identity's slot is freed. The three identity sites are the facade the root builds once (identity.Module, authoritative) and the two consumer-side shapes that let httpx stay identity-free (defense-in-depth). Identity's facade also re-exports the model's types and sentinels as aliases, so callers name identity's vocabulary without importing its internals; game's player handler mapping identity.ErrNotFound onto its own validation error is the only caller that depends on one of those sentinels. Tie: the copies cannot drift silently — TestImportBoundaries walks every import in the tree against internal/arch's allowedImports, so an httpx -> identity edge or a schedule -> identity edge fails the build's test first. No refusal of its own: the drift is a compile- or test-time failure, never a status a client sees — which is also why the layer value is approximate (the frozen enum has no composition-root layer; `handler` is the seam the facade satisfies). R-GAME-026 is the distinct publisher-language half of the same boundary at game's edge. stale-pointer: phase-05 §"As built" names `cmd/server` as the composition root and the hoisted `internal/idempotency`, and lists "api — the HTTP gateway" as a component; the binary is cmd/api, the idempotency engine is internal/kernel/idempotency and there is no api package (each module owns its transport). The doc's §"Suggested ownership" is already flagged in the extraction as superseded by its own §"As built".

### Entity `context_schema`

#### R-PLATFORM-012 — Each context owns one schema and speaks only as its own role

`implemented` · `boundary` · authority `composition-root`

Each bounded context owns one database schema and nothing else: the calendar its teams, seasons, rounds and matches; the game its rides, players and idempotency keys; identity its users, tokens, roles and permissions. A context reaches the database as its own role, which holds privileges on its own schema alone, so an attempt to read or write another context's tables is refused by the database itself rather than by code review.

**Authority** `composition-root` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000015_split_context_schemas.up.sql` | `db-constraint` | `authoritative` | — |
| `migrations/000018_context_roles.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/boundary_test.go::TestGameAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestScheduleAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestIdentityAppBoundary`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

#### R-PLATFORM-015 — A context's pool speaks as that context's role

`implemented` · `boundary` · authority `repository`

The connection pool a context is given runs every one of its statements as that context's role, and no context is ever handed another context's pool. The role is bound when a connection is established, together with the search path that resolves the context's own unqualified table names, so no statement can run with broader privileges than the context it serves.

**Authority** `repository` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `cmd/api/main.go::openContextPool` | `repository` | `authoritative` | — |
| `cmd/api/main.go::openPools` | `repository` | `authoritative` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/context_roles_test.go::TestContextPoolsRunAsContextRole`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

### Entity `cors_origin`

#### R-PLATFORM-005 — The cross-origin allowlist holds bare origins, never a wildcard

`implemented` · `orchestration` · authority `composition-root`

The deployment lists the origins it serves cross-origin, one bare origin each; a wildcard is refused at startup rather than degraded into "allow everything". Origins are normalized to lowercase scheme and host with any trailing slash removed, a value carrying a path, query or fragment is refused as not an origin, duplicates collapse, and an empty list means cross-origin access is simply off.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::parseCORSOrigins` | `composition-root` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParseCORSOrigins`
- unit: `internal/platform/config/config_test.go::TestParse`

### Entity `email_delivery`

#### R-PLATFORM-006 — Mail cannot be sent from an incomplete configuration

`implemented` · `orchestration` · authority `composition-root`

If the outbound mail settings are incomplete — no host, no credentials, no sender — the process refuses to start instead of accepting requests it cannot honour. The refusal is distinguished from a delivery failure, so a configuration mistake is never retried as if it were a network problem.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/mailer/mailer.go::New` | `composition-root` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrMissingCredentials` → _(falls through to 500)_

**Verification**

- unit: `internal/platform/mailer/mailer_test.go::TestNewRefusesIncompleteConfig`
- unit: `internal/platform/mailer/mailer_test.go::TestNewAcceptsCompleteConfig`
- unit: `internal/platform/mailer/mailer_test.go::TestSendFailureIsNotConfigError`

#### R-PLATFORM-007 — A failed delivery is retried a bounded number of times, then reported

`implemented` · `policy` · authority `service`

One email delivery is attempted at most three times, with a growing pause between attempts that is randomized so that many failing senders do not retry in a synchronized wave. A cancelled request stops the ladder at once instead of letting it run on after the caller and the process have given up.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/mailer/mailer.go::Mailer.Send` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/mailer/mailer_test.go::TestSendRetriesBoundedAttempts`
- unit: `internal/platform/mailer/mailer_test.go::TestSendJitterIsRandomized`
- unit: `internal/platform/mailer/mailer_test.go::TestSendStopsOnCancellation`

### Entity `http_request`

#### R-PLATFORM-014 — The request chain order is fixed

`implemented` · `orchestration` · authority `composition-root`

Every request traverses the same chain in the same order: panic recovery outermost, then cross-origin handling, then throttling, then credential resolution, then the route. That order is what guarantees a panic is always answered, a preflight never spends a throttle token and never needs a credential, and a caller is throttled before any work is done on its behalf.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/httpchain/httpchain.go::Build` | `composition-root` | `authoritative` | — |
| `cmd/api/routes.go::routes` | `handler` | `authoritative` | — |

**Verification**

- unit: `internal/platform/httpchain/httpchain_test.go::TestBuildOrder`
- e2e: `internal/e2e/cors_test.go::TestEnableCORS`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

### Entity `http_server`

#### R-PLATFORM-009 — Every connection is bounded in time

`implemented` · `orchestration` · authority `composition-root`

The HTTP server bounds how long it will spend on a connection: a read must finish within five seconds, a write within ten, and an idle connection is closed after a minute. A client that cannot meet those bounds does not hold a server resource indefinitely.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.Serve` | `composition-root` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/server_test.go::TestServeReadTimeoutClosesSilentConnection`
- unit: `internal/platform/server/server_test.go::TestServeWriteTimeoutClosesStreamingResponse`
- unit: `internal/platform/server/server_test.go::TestServeIdleTimeoutClosesKeepAlive`

### Entity `log_stream`

#### R-PLATFORM-008 — One structured log stream per process

`implemented` · `orchestration` · authority `composition-root`

The process emits one machine-readable log stream on standard output at the level the deployment chose, and everything that logs — the application and the database driver — writes into that same stream. Call-site source locations are attached in development only, so production records carry no path bulk.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/logging/logging.go::New` | `composition-root` | `authoritative` | — |
| `internal/platform/logging/logging.go::newLogger` | `composition-root` | `authoritative` | — |
| `internal/platform/logging/logging.go::Pgx` | `composition-root` | `authoritative` | — |

**Verification**

- unit: `internal/platform/logging/logging_test.go::TestNewJSONOutput`
- unit: `internal/platform/logging/logging_test.go::TestNewLevelFiltering`
- unit: `internal/platform/logging/logging_test.go::TestNewSourceShortFileInDevelopment`
- unit: `internal/platform/logging/logging_test.go::TestPgxLevelMapping`
- unit: `internal/platform/logging/logging_test.go::TestPgxDataBecomesSortedAttributes`

### Entity `process_lifecycle`

#### R-PLATFORM-010 — Shutdown is bounded and never hangs

`implemented` · `orchestration` · authority `service`

When the process is asked to stop, it stops accepting work, gives in-flight requests a fixed grace period to finish, and force-closes whatever is left when that period expires. A second stop request during the drain closes connections immediately, and after the drain the process waits a bounded time for background work before exiting. A drain that had to be forced is still a successful stop, not a failure.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.runServer` | `service` | `authoritative` | — |
| `internal/platform/server/server.go::Server.shutdown` | `service` | `authoritative` | — |
| `internal/platform/server/consts.go::ShutdownGracePeriod` | `service` | `authoritative` | — |
| `internal/platform/server/consts.go::BackgroundTaskBudget` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/shutdown_test.go::TestDrainTimeoutForceCloses`
- unit: `internal/platform/server/shutdown_test.go::TestSecondSignalForceClosesDuringDrain`
- unit: `internal/platform/server/shutdown_test.go::TestBackgroundTaskBudgetBoundsWait`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-PLATFORM-011 — Background work may fail without taking the process down

`implemented` · `policy` · authority `service`

Work that runs after a response has been sent — a mail delivery, a background sweep — is tracked by the server and runs under the process's cancellation root. If it fails or panics, the failure is recorded and the process keeps serving: once a response has been committed, no downstream task may kill it.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.Background` | `service` | `authoritative` | — |
| `cmd/api/main.go::main` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/server_test.go::TestBackgroundPanicIsContainedAndRecorded`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

### Entity `worker`

#### R-PLATFORM-016 — A failing background pass never kills the process

`specified-only` · `orchestration` · authority `composition-root`

A background pass that fails is logged and retried later: the failure never takes the process down and never stops the next pass from running.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-PLATFORM-017 — A background worker stops on cancellation and is waited for

`specified-only` · `orchestration` · authority `composition-root`

A background worker runs under the process's cancellation context and is waited for at shutdown: on a signal it stops taking new work, the process waits for what it already started, and only then exits.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`
