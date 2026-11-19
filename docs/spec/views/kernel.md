<!-- GENERATED — do not edit. -->
# Business rules — `kernel`

> **GENERATED — do not edit.** This is the `kernel` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/kernel/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**17 rules** — 14 implemented, 3 specified-only.

## Module `kernel`

### Entity `cors_origin`

#### R-KERNEL-010 — Cross-origin access is an exact allowlist, silently withheld otherwise

`implemented` · `policy` · authority `handler`

Only origins named in the deployment's allowlist are granted cross-origin access, by exact match. An unlisted origin receives no cross-origin permission at all — the request still completes, and the browser withholds the response — and a preflight from an allowed origin is answered immediately, before any throttle token is spent and before any credential is read.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.EnableCORS` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/cors_test.go::TestEnableCORS`

### Entity `db_budget`

#### R-KERNEL-005 — The database budget pays for storage, not for hashing

`implemented` · `orchestration` · authority `service`

A request's storage work is measured by one budget of three seconds, and that budget opens only around the database calls it pays for. Work that is not storage — a password hash on registration, a password comparison on login — runs outside it, so a slow machine cannot turn a healthy write into a timeout.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/persistence/consts.go::DBTimeout` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::Middleware.Authenticate` | `handler` | `defense-in-depth` | — |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `defense-in-depth` | — |

**Verification**

- unit: `internal/identity/internal/service/db_budget_test.go::TestRegisterHashesOutsideDBBudget`
- unit: `internal/identity/internal/service/db_budget_test.go::TestAuthenticateComparesOutsideDBBudget`

### Entity `domain_event`

#### R-KERNEL-013 — An event is a post-commit, same-process notification

`implemented` · `orchestration` · authority `service`

A domain event is delivered only after the publishing operation has committed, to subscribers inside the same process, in the order they subscribed. Nothing is stored and nothing is retried: a subscriber that fails, or a process that dies between the commit and the delivery, drops that event — the publisher's committed write stands and must not be rolled back.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/eventbus/eventbus.go::Bus.Publish` | `service` | `authoritative` | — |
| `internal/kernel/eventbus/eventbus.go::Bus.Subscribe` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/service.go::publish` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/round_lifecycle.go::RoundService.Close` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/kernel/eventbus/eventbus_test.go::TestBus_SubscribeAndPublish`
- unit: `internal/kernel/eventbus/eventbus_test.go::TestBus_PublishReturnsFirstHandlerError`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

#### R-KERNEL-014 — A subscriber claims the event before it acts

`implemented` · `orchestration` · authority `service`

A subscriber records the event's identity before it writes anything, so an event delivered twice is handled once: the second delivery finds the claim and completes as a no-op. A subscriber that fails leaves no claim behind, so the same event may be delivered and processed again.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/settlement.go::Service.runEvent` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.OnMatchResultRecorded` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted` | `service` | `authoritative` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

### Entity `http_request`

#### R-KERNEL-006 — Request validation is collected, not fail-fast

`implemented` · `interface` · authority `handler`

A malformed request reports every offending field at once, one message per field, so the caller can fix the whole request in one round trip. A request that fails no field check is the only one that reaches the operation behind the handler.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/validator/validator.go::Validator.AddError` | `validator` | `authoritative` | — |
| `internal/kernel/validator/validator.go::Validator.Check` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- e2e: `internal/e2e/teams_test.go::TestCreateTeamHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

#### R-KERNEL-008 — A malformed credential is refused before the handler

`implemented` · `interface` · authority `handler`

A request that presents a credential must present it as a bearer token; anything else — an unknown scheme, a missing value — is refused as unauthenticated before any handler runs. A request that presents no credential at all is not refused here: it proceeds as an anonymous caller and is judged by the permission gate.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.Authenticate` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::InvalidAuthenticationToken` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/context.go::SetAuthenticatedUser` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidToken` → `401`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-KERNEL-011 — A panic is answered as a server error, never explained

`implemented` · `interface` · authority `handler`

A failure inside the request chain is answered with the generic server-error message and the connection is closed; the cause is recorded in the log and never returned to the caller. No internal detail — a stack, a driver message, a query — reaches a client through this path.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RecoverPanic` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::ServerError` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `500`

**Verification**

- unit: `internal/kernel/httpx/middleware_test.go::TestRecoverPanicAnswersServerError`

#### R-KERNEL-012 — A request body is read under a one-megabyte cap

`implemented` · `interface` · authority `handler`

No request body is read beyond one megabyte. A body over that limit is refused as too large before anything is decoded or deduplicated, so an oversized request claims no idempotency key and a retry with a normal body executes normally.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/json.go::MaxBodyBytes` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/json.go::ReadJSON` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `413`

**Verification**

- e2e: `internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge`

### Entity `idempotency_key`

#### R-KERNEL-001 — An idempotent command must carry a usable idempotency key

`implemented` · `interface` · authority `handler`

Every command that promises retry safety must arrive with a key that names one logical request: a non-empty value of at most 255 bytes. A request without one is refused as a malformed request, and no writer — a script, a future service — can store a key outside those bounds.

**Authority** `handler` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/idempotency/idempotency.go::ValidateIdempotencyToken` | `validator` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → `idempotency_keys_key_check` → _(unmapped)_ → `422`

**Verification**

- unit: `internal/kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

#### R-KERNEL-002 — A request's dedup identity is a digest of the request itself

`implemented` · `orchestration` · authority `handler`

Two requests are the same logical request exactly when they carry the same method, the same request URI and — for a command with a body — the same raw body bytes. The identity is a SHA-256 digest of exactly those bytes, and a body is capped before it is hashed so that the bytes hashed are the bytes decoded: a retry of an identical request is recognized as a replay, any changed field makes it a different request, and an oversized body claims no key at all.

**Authority** `handler` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.rideCommandToken` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken` | `handler` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge`

#### R-KERNEL-003 — An idempotency key names exactly one logical request

`implemented` · `policy` · authority `repository`

Once a key has been used by one caller, that same caller reusing it for a different request is refused as key misuse and the second request never executes. The refusal is a conflict, not a retry: a genuine retry resends the identical request and receives the stored answer. A key belongs to its caller — two callers that happen to choose the same key hold two independent claims, so one caller's key can neither collide with another's nor come back with another's answer. A claim is remembered for one day: a key whose claim is older than that is forgotten, and a command sent under it executes again instead of replaying.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `authoritative` | — |
| `migrations/000022_scope_idempotency_keys_per_caller.up.sql` | `db-constraint` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |
| `internal/game/internal/adapter/inbound/http/errors.go::Handler.idempotencyConflictResponse` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrIdempotencyConflict` → `409`

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency`
- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

#### R-KERNEL-004 — A claim commits with its command, never before or after it

`implemented` · `orchestration` · authority `service`

The claim on a key and the stored answer are written inside the command's own transaction, so a command that fails leaves no claim behind (failures are never cached) and a command that succeeds commits its claim together with its effect. A concurrent duplicate waits on the claim and then receives the committed answer instead of executing a second time.

**Authority** `service` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/ride_commands.go::Service.RideCreate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideLock` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideBurn` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideUnlock` | `service` | `authoritative` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `defense-in-depth` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Save` | `repository` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency`
- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

### Entity `list_query`

#### R-KERNEL-007 — A list request is bounded, and its sort never becomes client SQL

`implemented` · `interface` · authority `handler`

A list request selects a bounded window of results — at least one row, at most one hundred per page, and a page number small enough to be addressable — and it may sort only by a column the resource publishes. A sort name outside that list is refused as a malformed request rather than handed to the database.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/persistence/filters.go::ValidateFilters` | `validator` | `authoritative` | — |
| `internal/kernel/persistence/filters.go::Filters.SortColumn` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- e2e: `internal/e2e/teams_test.go::TestListTeamsHandler`
- e2e: `internal/e2e/rides_test.go::TestListRidesHandler`

### Entity `outbox_event`

#### R-KERNEL-015 — An event is committed with the change that caused it

`specified-only` · `orchestration` · authority `service`

The fact that an event must be published is committed in the same transaction as the state change itself: if the change commits the event is owed, and if the change rolls back the event was never owed.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

#### R-KERNEL-016 — A relay delivers committed events and marks them published

`specified-only` · `orchestration` · authority `service`

Events owed by committed transactions are read from the outbox by a relay that runs outside the request, delivered to their consumers, and marked published only after the delivery, so a crash leaves the event to be delivered again.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

#### R-KERNEL-017 — Delivery is at-least-once and never claimed exactly-once

`specified-only` · `orchestration` · authority `service`

An event a transaction committed to publish is eventually delivered at least once, and the system makes no exactly-once claim: a consumer must tolerate the same event arriving twice.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

### Entity `rate_limit`

#### R-KERNEL-009 — Each client gets a token bucket, and overdraw is a 429

`implemented` · `policy` · authority `handler`

Requests are throttled per client address: a client may burst a configured number of requests and is then refilled at a configured rate. A client over its allowance is refused with a rate-limit answer that tells it how long to wait, and the operation behind the gate never runs.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.Allow` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.ClientKey` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::Middleware.RateLimit` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.Sweep` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `429`

**Verification**

- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimitMiddlewareDenial`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterSustainedCapped`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterIPsIsolated`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterStaleBucketsEvicted`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyUsesForwardedClient`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyTakesRightmostUntrustedHop`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyUntrustedPeerIgnoresForwardedFor`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyEmptyTrustedListIgnoresForwardedFor`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyMalformedForwardedForFallsBack`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestRateLimitMiddlewareKeysOnForwardedClientBehindProxy`
