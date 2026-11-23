# AGENTS.md

## Docs
- `docs/league-tokens-learning-phases` is the source of truth for what to build and why. Never preload: read the smallest subset the task needs, then stop.
- `docs/league-tokens-learning-phases/domain-knowledge.md` and `domain-knowledge-game-engine.md` are the domain source of truth; the phase guides, rule catalog and features derive from them; on conflict the domain sources win and the rule is drift to fix.

## Spec
- The executable specification lives in `docs/spec/`: `docs/spec/rules/` is the canonical catalog, one authored markdown file per rule (YAML front matter + notes body); `features/` is the Gherkin; `docs/spec/coverage.md` is the generated thin coverage dashboard — never hand-edit it. `cmd/spec` is the checker; `docs/spec/README.md` is the workflow, `docs/spec/rules.schema.md` the format authority.
- Rule-change protocol: `docs/spec/README.md` §1 (how to change a rule) — change the rule's file and its scenario first, audit red, implement, `make spec/gen`, audit green. Nothing lands without the gate.
- `make audit` is the single source of truth: a stale `coverage.md` fails it, and `spec/check` runs `--strict`, so any policy error fails it too.
- `make audit` (the full gate) needs `LT_API_TEST_DSN` — `make db/up` starts Postgres; `make audit/static` is the DB-free half.
- Delegated work: `make spec/work args='<ID>'` output is the task packet, exactly one `SPEC-SCOPE:` line declares the worker's scope, the worker's own gates (scoped check, `gofmt`, `make audit/static`, `make spec/gen` when the catalog changed) are advisory, and the host owns `make audit` plus `make prove` before a merge (`docs/spec/README.md` §9, what the spec loop depends on).
