# Executable specification — front door

`docs/spec/` is lt-api's executable specification. The catalog says what every
business rule is and who decides it; the scenarios say what those rules look like from
outside; the checker keeps catalog, code, tests and scenarios tied to each other; the
audit gate refuses a stale or contradictory spec. This file is the workflow.
`rules.schema.md` is the format authority; `plan.md` is the plan.

## 1. The pieces

- **Catalog.** `docs/spec/rules.yaml` is the canonical, hand-edited catalog. The readable
  view `docs/spec/business-rules.md` is **generated** from it — never hand-edit; only the
  YAML feeds it.
- **Scenarios.** `docs/spec/features/<module>/<capability>.feature` holds one capability
  of business-visible behaviour per file. Every scenario carries exactly one
  `@rule:R-...` tag. Authoring rules: `gherkin-style.md`.
- **Checker.** `cmd/spec`: `gen` regenerates the Markdown (or `--diff` to detect
  staleness); `check` validates the catalog's schema policies and traces every site,
  test pointer and Gherkin pointer to something that exists, in both catalog → tree and
  tree → catalog directions. `--strict` makes any check error fatal.
- **Enforcement suite.** `internal/spec` is a godog suite run as a plain `go test`
  against real Postgres — real migrations, role-restricted pools, the product's own
  entry points, plus sabotage steps for database-authority proofs. Scenarios tagged
  `@known-defect:<n>` are excluded by default; `SPEC_KNOWN_DEFECTS=1` runs them.

## 2. Rule-change protocol

A rule change starts in the spec and goes red before it lands green:

1. Change the catalog entry in `docs/spec/rules.yaml` **and** its scenario under
   `docs/spec/features/**` first (new rule → schema §5).
2. Run `make audit`: the gap between the spec and the code is the failure — the
   scenario is red (and so is an untraceable catalog entry, since `spec/check` runs
   `--strict`).
3. Implement the change.
4. Run `make spec/gen` to regenerate `docs/spec/business-rules.md`.
5. Run `make audit` again; it is done when green. Nothing lands without this gate.

Never edit `business-rules.md`; never add an enforcement site without updating the
catalog entry that names it.

## 3. Running each piece

| target | what it does |
|---|---|
| `make spec/gen` | regenerate `docs/spec/business-rules.md` from `rules.yaml` |
| `make spec/check` | validate schema policies + catalog/feature traceability; runs `--strict`, so any check error fails the target (and therefore `make audit`) |
| `make spec/diff` | fail if `business-rules.md` is stale (wired into `make audit`) |
| `make test/spec` | run the godog scenarios against real Postgres; skips without `LT_API_TEST_DSN` |
| `make test` | run the full test tree; DB-backed tests skip when `LT_API_TEST_DSN` is unset |
| `make audit` | the single gate: `tidy -diff` → `mod verify` → gofmt → vet → staticcheck → `spec/diff` → `spec/check` → full test tree |

Set a real `LT_API_TEST_DSN` (`.envrc`) before trusting a green run: without it the
database-backed half skips.

## 4. Agent quickstart

**Picking up a task.** Find the rule in `docs/spec/rules.yaml`; read its `statement`,
`enforcement`, `verification` and `notes`, then open the feature file it points at.
Implement against the behavior those two describe. Run `make audit`; done when green.
If the behavior must change, change the catalog and scenario first (§2).

**Adding a rule.** Follow `rules.schema.md` §5: pick the authority and id, verify every
site and test against the code today, add the catalog entry and its `@rule` scenario,
run `make spec/gen`, then `make audit`.

**Adding a scenario to an existing rule.** One scenario, one rule; name it so
`<feature-file>::<scenario name>` is the pointer in `verification.gherkin`. Reuse
`gherkin-style.md` vocabulary; no transport or SQL words in facade steps.
