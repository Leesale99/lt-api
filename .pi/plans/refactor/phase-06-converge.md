# Phase 6 — converge: delete internal/api, E2E suite → shared/server, sentinels out, arch final, cmd/api final

1. Delete `internal/api` production code entirely. The `/v1/healthcheck` route keeps its phase-2 home (httpx or cmd/api inline — pick one, route identical).
2. E2E test migration: api's `testmain_test.go` (4-pool harness), `seed_test.go` (reset fixture), and the matches/rounds/seasons/teams/players/rides/users/auth/shutdown tests → `internal/shared/server/*_test.go`. Compose real modules exactly like cmd/api/main.go via ONE test composition helper mirroring main (the old api/players_test.go `newTestApp` duplicate dies). Keep the same Test function names (coverage guard). `auth_test` now exercises `httpx.authenticate` against the identity facade — preserve what each test actually covered.
3. Sentinel removal: delete `persistence.ErrRecordNotFound` & friends; grep first — no importer may remain (phases 3–5 migrated their control flow; repositories own pg-error → module-sentinel translation). pg classifiers stay.
4. `internal/arch` final matrix: remove the "api" special-casing; classes: `shared:*`, schedule, game, identity, ledger, idempotency, cmd. Enforce: `shared:*` imports only `shared:*`; module internal packages import own module + `shared:*` + other module ROOTS only; tests included as today. `TestImportBoundaries` must PASS against the final graph AND catch a deliberately-injected violation (inject one, watch it fail, revert).
5. `cmd/api/main.go` final shape — explicit composition (vault Phase 05 task 7 spirit): config → logging → mailer → pools → bus → schedule.NewModule → game.NewModule (+ACL) → identity.NewModule → eventbus.Subscribe wiring (incl. the async ACL wrappers from phase 4) → middleware chain (httpx) → server.Server → Serve. Readable line-by-line, no magic helpers.
6. Grep guards (clean, or explicitly whitelisted in the report): `internal/api` (0 hits), `cmd/server` (0 outside untouched historical paths), `type \w*Store struct` (0), `\bNewStore\b` (0), bare `store\.` qualifiers (only `persistence.` remains).

## Done when
Invariants hold incl. the full DB suite if LT_API_TEST_DSN is available; commits `refactor(6): …`.
