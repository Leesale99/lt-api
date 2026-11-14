# Phase 2 — shared kernel: httpx, server, persistence; arch path-aware

Goal: carve reusable HTTP machinery + process lifecycle out of `internal/api`; move `internal/store` → `internal/shared/persistence`; make `internal/arch` path-aware so the nested module packages created in phases 3–5 are **enforced**, not silently skipped.

After this phase `internal/api` SURVIVES as: routes.go + all handlers + domain error mappers + MailSender + test composition. It imports httpx/server from now on; it dies in phase 6.

## 2a. internal/store → internal/shared/persistence
- `git mv internal/store internal/shared/persistence`; package `store` → `persistence`; update all importers (~31 files; also the `storepkg` alias in internal/identity — normalize to plain `persistence`).
- Contents unchanged (Querier, Filters/Metadata, pg classifiers, sentinels). Sentinels stay until phase 6.

## 2b. carve shared/httpx from internal/api
Move code, keep behavior identical:
- `helpers.go` → `httpx/json.go` (envelope, writeJSON, writeRawJSON, readJSON, readString/readCSV/readInt, background), `httpx/params.go` (readIDParam, sort safelists), `httpx/filters.go` (readListFilters → persistence.Filters, writeListResponse).
- `context.go` → `httpx/context.go` (request-context keys + accessors, unchanged semantics).
- `middleware.go` → `httpx/middleware.go`: `recoverPanic`, `enableCORS`, `rateLimit`, `requirePermission` move as-is. `authenticate` changes shape: today it calls `app.Identity.Users.GetForToken`. httpx must NOT import identity (shared rule), so define in httpx: `type AuthUser struct{...}` (exactly the fields auth actually uses) and `type UserLookup interface { UserForToken(ctx, token) (AuthUser, error) }` — consumer-side interface. `authenticate(lookup)` stores AuthUser in the context; every permission check reading the context keeps working. `internal/api` provides a temporary adapter around `identity.Store` satisfying UserLookup (identity's User → AuthUser mapping); it is replaced by the identity facade in phase 5.
- `ratelimiter.go` + `ratelimiter_test.go` + `cors_test.go` → httpx (pure unit tests move now).
- `errors.go` → split: **generic** status writers (not-found, method-not-allowed, conflict, unprocessable, too-many-requests, server-error, generic bad-request/validation envelope) → `httpx/errors.go`. **Domain-flavored** writers (roundNotOpen, noNextMatch, invalidPhase, invalidStateTransition, idempotencyConflict, anything tied to a module's error types) STAY in internal/api; they migrate with their modules in phases 3–5.
- `healthcheck.go`: route `/v1/healthcheck` unchanged; the handler may live in httpx or stay in api — either, be consistent.

## 2c. carve shared/server from internal/api/server.go
- `server.go` → `internal/shared/server/server.go`: type `Server` (net/http.Server stutter precedent) — or keep `Application`; pick one, stay consistent. Owns: Version/Port/Env/Logger/RootCtx, the `http.Server`, `RunOptions`, `Serve`, `runServer` (signal→drain→force-close→bounded wg wait), `shutdown`, background-task wg. It receives a fully-built `http.Handler` (router + middleware) and knows nothing about modules.
- `shutdown_test.go` → `shared/server` (adapt construction; keep both tests).
- `MailSender` and the module-dep fields (Game/Schedule/Identity/Mailer) STAY in internal/api (they belong to handlers). api builds the handler chain (constructor or Routes func returning http.Handler); cmd/api/main.go creates the server and calls Serve. Keep the explicit composition style.

## 2d. internal/arch path-aware (fix the blind spot)
Current bug: `internalPkg`/`classify` (arch_test.go:228–257) only see leaf dir names and SKIP any path with another `/` after `internal/` — every nested package from phases 3–5 would be invisible. Rewrite classification to be path-aware:
- `class("internal/shared/X") = "shared:X"`; `class("internal/<module>/…") = "<module>"` (any depth, incl. nested `internal/`); `class("cmd/X") = "cmd"`.
- Matrix keyed on classes; preserve every edge allowed today (former `store`/`validator`/`eventbus`/… leaves become `shared:X`; the "api" special-casing at :160–161/:187–189 stays while internal/api exists).
- New rules enforced from now on: `shared:*` imports ONLY other `shared:*` (never a module); module roots import `shared:*` + other module roots. Must already hold after 2a–2c — verify with the test.

## Done when
Invariants hold; `grep -rn "internal/store"` → 0 (imports and comments); no shared package imports a module (arch test green); suggest commits: `refactor(2a): …`, `refactor(2b): …`, `refactor(2c): …`, `refactor(2d): …`.
