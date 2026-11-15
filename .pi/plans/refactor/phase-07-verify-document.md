# Phase 7 — verify, document, vault, AWAITING_APPROVAL

1. Full verification in the worktree: `make audit` (gofmt, vet, staticcheck, `go test -race -vet=off ./...`) with `LT_API_TEST_DSN` set. If the DSN isn't in the job env, source `../lt-api/.envrc` (parent checkout) for the run only — never copy or commit it. If still unavailable: run everything except the DB suites and SAY SO prominently in the approval block.
2. Route-table diff: `git show 921c98e:internal/api/routes.go` vs the final route registrations (all modules + healthcheck) — identical set of 47 routes: method + path + middleware. Include the diff result (or "identical") in the report.
3. Test inventory: count Test functions before (baseline 921c98e) vs after; list any that were only moved/renamed; no test deleted without a stated reason.
4. README: rewrite the repo-structure section for the new layout (tree from 00-plan) + composition-root wording (cmd/api).
5. Makefile sanity: `run/api`, `build/api`, `test/unit` (phase 1), `audit` all work; `make help` output matches.
6. Vault (delegate to librarian — the vault is outside the repo, unaffected by the worktree): update `Phase 05 - Modular monolith boundaries.md` per the lt-api-kb skill rules: as-built note — cmd/api rename, module facades + nested internal/, shared/ split (httpx/server/persistence), path-aware arch matrix; task 7's wording now refers to cmd/api/main.go; DO NOT close Phase 05 (tasks 2 & 4 remain open). Update Home.md's Now section only if the skill's rules require it.
7. Assemble AWAITING_APPROVAL exactly per 00-plan §Handoff and END the round. Terminal line: AWAITING_APPROVAL.
