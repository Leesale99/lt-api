# AGENTS.md

## Docs
- `docs/league-tokens-learning-phases` is the source of truth for what to build and why. Never preload: read the smallest subset the task needs, then stop.

## Spec
- The executable specification lives in `docs/spec/`: `rules.yaml` is the canonical catalog, `features/` is the Gherkin, `business-rules.md` is generated — never hand-edit it. `cmd/spec` is the checker; `docs/spec/README.md` is the workflow, `rules.schema.md` the format authority.
- Rule-change protocol: see `docs/spec/README.md` §2 — change the catalog entry and its scenario first, audit red, implement, `make spec/gen`, audit green. Nothing lands without the gate.
- `make audit` is the single source of truth: a stale `business-rules.md` fails it, and `spec/check` runs `--strict`, so any policy error fails it too.
