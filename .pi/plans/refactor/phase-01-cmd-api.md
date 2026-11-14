# Phase 1 — rename cmd/server → cmd/api (mechanical)

Goal: directory + reference rename only, no logic changes. The wiring rewrite happens in phase 6.

## Steps
1. `git mv cmd/server cmd/api`.
2. Makefile:
   - target `run/server` → `run/api`, path `./cmd/server` → `./cmd/api` (its doc comment already says `run/api` — now they agree).
   - `build/api`: both build lines → `./cmd/api` (outputs `bin/api`, `bin/linux_amd64/api` are already correct).
   - `test/unit` is stale: it runs `-run 'TestValidateMatch|TestWinner'` but `TestValidateMatch` no longer exists (real names: `TestValidateNewMatch`, `TestValidateMatchUpdate` in internal/schedule). Replace with a fast pure-logic target, e.g. `go test ./internal/schedule/... -run 'TestValidateNewMatch|TestValidateMatchUpdate|TestWinner|TestPhase' -v` — verify the names exist and pass at execution time.
3. README.md: ~line 31 (tree entry) and ~line 97 ("composition root") → cmd/api.
4. Comment-only path mentions (update text, no code change): `internal/testdb/testdb.go:4`, `cmd/seed/main.go:22`, `internal/api/players_test.go:33`, `internal/schedule/rounds_store_test.go:184`, `internal/identity/users_test.go:33`.
5. Leave untouched: `docs/league-tokens-learning-phases/**`, `.pi/**`, `scratch/**`.

## Done when
Invariants hold; `grep -rn "cmd/server"` outside the untouched paths returns nothing; commit `refactor(1): rename cmd/server to cmd/api`.
