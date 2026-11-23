# AGENTS.md

## Docs
- `docs/league-tokens-learning-phases` is the source of truth for what to build and why. Never preload: read the smallest subset the task needs, then stop.
- `docs/league-tokens-learning-phases/domain-knowledge.md` and `domain-knowledge-game-engine.md` are the domain source of truth; the phase guides, rule catalog and features derive from them; on conflict the domain sources win and the rule is drift to fix.

## Spec
- The executable specification lives in `docs/spec/`: `docs/spec/rules/` is the canonical catalog, one authored markdown file per rule (YAML front matter + notes body); `features/` is the Gherkin; `docs/spec/business-rules.md` is the generated thin dashboard — never hand-edit it. `cmd/spec` is the checker; `docs/spec/README.md` is the workflow, `rules.schema.md` the format authority.
- Rule-change protocol: see `docs/spec/README.md` §1 — change the rule's file and its scenario first, audit red, implement, `make spec/gen`, audit green. Nothing lands without the gate.
- `make audit` is the single source of truth: a stale `business-rules.md` fails it, and `spec/check` runs `--strict`, so any policy error fails it too.
- `make audit` (the full gate) needs `LT_API_TEST_DSN` — `make db/up` starts Postgres; `make audit/static` is the DB-free half.
- Rule → `spec work` → gates flow for delegated work: `docs/spec/README.md` §9.
