# Executable Specification — Working Plan

> Living checklist. The orchestrator updates it at every gate.
> `docs/spec/rules.schema.md` is the source of truth for the catalog format and enums.

**Current state:** Phase B closed (B5 approved); C-pilot running.

## Goal

A single, machine-checkable statement of every business rule in lt-api (the catalog),
expressed as Gherkin scenarios where business-visible, executed against real Postgres,
and enforced end-to-end by `make audit` — so that any change to a rule starts in the
spec, goes red, and cannot land green without satisfying the specification.

## Locked decisions

| # | Decision |
|---|---|
| 1 | Catalog: YAML canonical (`docs/spec/rules.yaml`) + generated markdown (`docs/spec/business-rules.md`) |
| 2 | Scenarios bind to module facades over real DB (`testdb`: real migrations, real roles); sabotage steps via Owner pool for DB-authority proofs |
| 3 | Gherkin scope: business-visible rules only (~45–60 of the 108 implemented business-visible rules); mechanical field rules stay unit-tested but catalogued |
| 4 | Framework: godog; suite in `internal/spec`, runs as plain `go test` |
| 5 | Repo canonical; vault mirrors + captures Lessons |
| 6 | ID scheme: `R-<MODULE>-NNN` flat per module (`schedule` \| `game` \| `identity` \| `ledger` \| `kernel` \| `platform`) |
| 7 | Enforcement sites: `path::Symbol` (Go) or migration id (SQL) — never line numbers |
| 8 | Base: `main` @ `d157aa1` (post PR #7 merge). Local `LT_API_TEST_DSN` available to workers |
| 9 | Catalog enums (category / authority / mechanism / scope / layer / role / status / module) are frozen in `docs/spec/rules.schema.md`, derived from the vault Lessons vocabulary — that doc is authoritative for values |

## Artifacts

| Path | What |
|---|---|
| `docs/spec/rules.yaml` | canonical catalog (hand-edited) |
| `docs/spec/business-rules.md` | GENERATED from rules.yaml — never hand-edited; audit fails if stale |
| `docs/spec/rules.schema.md` | catalog schema + conventions (exists — written in A1) |
| `docs/spec/gherkin-style.md` | scenario authoring conventions (C1) |
| `docs/spec/features/<module>/<capability>.feature` | Gherkin scenarios, tagged `@rule:R-...` |
| `docs/spec/README.md` | front door: human & agent workflow (E2) |
| `internal/spec/` | godog suite: TestMain(testdb) + fixtures/sabotage DSL + step defs |
| `cmd/spec/` | `gen` (yaml→md, `--diff` mode) and `check` (schema + traceability) |

Audit order after Phase D: `tidy -diff` → `mod verify` → `gofmt` → `vet` → `staticcheck`
→ `spec/gen --diff` → `spec/check` → unit → spec → db tests. CI workflow unchanged
(audit stays the single source of truth).

## Workflow

- **Gates (user):** A2 template · B5 catalog + misalignment report · C3 pilot readability ·
  C7 coverage · D2 green CI · E5 sabotage demo.
- **Small phases (A, E):** foreground workers, in-session.
- **Fan-out phases (B, C+D):** one background orchestrator job per phase, `HANDOFF=confirm`,
  dedicated worktree, stops at the gate with AWAITING_APPROVAL.
- **Roles:** scout = extraction · general = authoring/merge/review · go-worker = code ·
  researcher = version pinning · librarian = vault.
- Workers verify their own output (the relevant audit slice) or report that they couldn't.

## Phases

### Phase A — Vocabulary & skeleton
- [x] A0 librarian: taxonomy from vault Lessons (4 notes) → category vocabulary
- [x] A1 general: `rules.schema.md` + seed `rules.yaml` (12 ride/ledger rules, all 3 statuses)
- [x] A2 **GATE**: user approves template, taxonomy, IDs + A2 decision list
      (authority enum, drifted semantics, enum extensions, granularity principle,
      refusal-mapping, R-GAME-004 gap) (approved: D1 five-value authority enum + store→repository rename; D2-a drifted = content-only, stale-pointer notes; D3 validator layer + composition-root authority; D4 granularity principle; D5 refusal convention; D6 defer R-GAME-004 fixture)

### Phase B — Extraction & reconciliation
- [x] B1 scout×4: schedule 30 · game 19 (−2 merged/freed) · identity 22 (−1) · kernel+platform 30 fragments (all sites/tests verified)
- [x] B2 scout: phase docs 00–08 sweep → 17 specified-only fragments + doc→implemented map
- [x] B3 general: 127-rule catalog (12 seeds + 115 merged, 4 dedupe merges, dense renumbering) + six-section misalignment report (0 drifted, 14 stale-pointer, 19 specified-only, 74/108 undocumented, 9+24 verification gaps, 21 defect candidates)
- [x] B4 go-worker: `cmd/spec` gen + check (report-only; --strict unwired for Phase D), tests green, business-rules.md generated in sync
- [x] B5 **GATE**: user reviews catalog + misalignment report
      (approved: dupe policy — one statement per rule, N enforcement sites; kernel owns generic mechanisms, module call-sites become enforcement sites on the kernel rule; C-pilot confirmed)

### Phase C — Gherkin authoring (harness interleaved)
- [x] C1 general: `gherkin-style.md` + pilot features (ride lifecycle, settlement)
- [x] C2 go-worker: `internal/spec` skeleton (TestMain + testdb + DSL + sabotage) + pilot
      steps + Makefile targets; researcher pins godog/yaml versions first
- [ ] C3 **GATE**: pilot readability ("could a non-Go reader verify intent?")
- [ ] C4 general×3: features per capability (schedule lifecycle/freeze; identity
      authz+activation; game players+idempotency)
- [ ] C5 go-worker×3: step impls, red→green (local DSN)
- [ ] C6 general: vocabulary cross-review + coverage report
- [ ] C7 **GATE**: readability sample + coverage numbers

### Phase D — Automation & CI
- [ ] D1 go-worker: `audit` extended; CI green run
- [ ] D2 **GATE**: green audit incl. spec on CI

### Phase E — Enforcement closure
- [ ] E1 go-worker: `spec check` strict (unverified rule fails audit)
- [ ] E2 general: `docs/spec/README.md` + AGENTS.md rule-change protocol
- [ ] E3 librarian: vault mirror + Lessons; file Deferred/docker-compose
- [ ] E4 go-worker: sabotage demo (one violation per layer → audit catches each)
- [ ] E5 **GATE**: demo passes → pipeline is the executable specification

### Phase F — Pilot: Phase 06 ledger, spec-first
Separate roadmap effort; uses this pipeline (scenarios red before implementation).
R-LEDGER-001/002 in the seed catalog are the first two entries of this phase.

## Deferred / backlog (out of scope here)

- **docker-compose for local Postgres** — user wants this immediately after this task;
  E3 also files it in the vault Deferred.
- Coverage profile + target; mutation testing on domain packages; golangci-lint adoption.
- `rule:` code annotations + two-way check; `cmd/spec sync` (re-resolve enforcement sites).
- R-GAME-004 follow-ups: direct DB test for the rides money CHECKs; map Postgres `23514`
  (check_violation) to a 4xx instead of 500 (defect candidate — also lands in the B3
  misalignment report).
