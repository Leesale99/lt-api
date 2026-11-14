# Phase 5 — identity module (layered) + ledger/idempotency light alignment

Same layered shape as schedule: `internal/identity/internal/{model,repository,service,handler}` + facade `identity.go`.

## identity
- `users.go` → `model/user.go` + `repository/user_repo.go` (UserStore → UserRepository).
- `user_tokens.go` → `model/user_token.go` + `repository/user_token_repo.go`.
- `activation.go` → `service/activation.go` — takes a `Mailer` interface DEFINED HERE (the api/server.go MailSender dies); `internal/shared/mailer` satisfies it structurally.
- `permissions.go`, `roles.go` → `model/` (vocab); the facade exposes whatever httpx/cmd need.
- `store.go` → **DIES**; facade `identity.go`: Module, NewModule(pool, mailer), RegisterRoutes, and `UserForToken` implementing `httpx.UserLookup` natively (replaces the phase-2 temporary adapter in api; the AuthUser mapping lives here).
- identity sentinels: model errors replace persistence sentinels for identity-internal control flow; the auth path maps identity.ErrNotFound → 401 exactly as today.
- handlers `api/users.go`, `api/user_tokens.go` → `identity/internal/handler/` (+ dto.go, tags verbatim; errors.go mappers; routes.go). `/v1/healthcheck` stays wherever phase 2 put it.
- Update the arch matrix for the new identity packages.

## ledger (light)
- `store.go` → `postgres.go`; `Store` → `Repository`; `NewStore` → `New`. Thin facade surface; the Ledger interface stays the contract (game consumes it via its own port from phase 4). No layout change beyond that.

## idempotency (light)
- `IdempotencyStore` → `Repository` (idempotency.go), constructor rename, no layout change.

## Tests
- `users_test.go`, `user_tokens_test.go`, `activation_test.go` → model/repository/service as appropriate (harness moves with them).
- `api/users_test.go` stays for the phase-6 E2E move.
- cmd/api wiring; internal/api drops identity handlers/routes (hot files rule). After this phase internal/api contains at most: an empty routes shell, the testmain/seed fixture/E2E tests — nothing else production-wise.

## Done when
Invariants hold; identity self-contained (model has no pgx); httpx auth satisfied by the identity facade; no Store identifiers in identity/ledger/idempotency; commits `refactor(5): …`.
