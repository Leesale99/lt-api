# Phase B2 — policy registry, scoped-check fix, spec work

HANDOFF=confirm. SPEC-SCOPE none (policy engine + commands; no rule content changes).

## B2.1 — policy registry

- `policies.go` (new): `[]Policy{ID, Name, Level, Scopeable, Run}` — an ordered literal table replacing the 18 methods + `policyIndex`. Numbering becomes contiguous: the gap at 17 closes (`refusal` → `17-refusal`; `18-oracle` stays). One print path; the `printGherkinSection` special case dies (Level drives presentation).
- Docs renumbered: `rules.schema.md` §3, `docs/spec/README.md` policy table.

## B2.2 — scoped-check behavior fix (defect 1; approved as a behavior change)

- Vocabulary findings: `Subject` = the scenario's rule id (via `featureIndex.byRule`) → scopeable. Annotation-reverse findings: subject = rule id when resolvable, else catalog-level.
- Scoped runs (`--module`/`--rule`): rule-level findings filtered as today; **catalog-level and feature-level findings are always reported** (catalog-level as WARN) — a worker's gate stays strict but is no longer blind.
- `check_scope_test.go`: flip the pinned `""` case; add a case proving a scoped run FAILS on a vocabulary violation inside the scoped rule's scenarios, and on a malformed annotation pointing at the scoped rule.
- Guarantee test (the `internal/arch` pattern): mutate a fixture (bad step word in a scoped rule's scenario; unknown-id annotation) → scoped run must ERROR.

## B2.3 — `spec work` (brief + queue merge; decision 10)

- One command: `spec work` (table: drifted → specified-only → broken-pointer), `spec work <ID>` (per-rule work order), `spec work --module m`. `brief.go` + `queue.go` → `work.go`.
- One rule-block renderer: the overlay only (enforcement/verification resolution tables, linked scenarios, gate commands) — the authored prose lives in the rule file's body since B1.
- `queue --json` dropped (zero consumers; re-add only if delegate automation later needs packets — then via B3's `protocol.ts`).
- References updated: Makefile (`spec/brief`/`spec/queue` → `spec/work`), `docs/spec/README.md` §3/§5, `.pi/prompts/implement.md`, persona files, `AGENTS.md`.

## B2.4 — parse dedup

- `vocabulary.checkStepVocabulary` + `oracle.parseOracleScenarios` consume `featureIndex` (two full feature-tree parses per run die).
- `prove.goAnnotationIDs` → the annotations lexer (`annotationIDsOf` with a contiguity flag); one `//rule:` lexer total.
- Module→rules expansion: all call sites → `Scope.RuleIDs`.

## B2.5 — field hygiene verification

- `Entity`: kept as descriptive-only (single consumer: work's field table); document in `rules.schema.md` §2 as descriptive. Dropping was considered — churn > value.
- `write_scope`, `stale_pointer`, `tie`: policies 5/7/8 updated for the promoted fields; `check --strict` green over the migrated catalog.

## Acceptance

`make audit` green; the new scoped-blindness tests green; `spec work <sample>` renders the same information the old brief did (spot-check one rule per module); no extension code touched → smoke not required, but run it anyway if `make smoke` is cheap.
