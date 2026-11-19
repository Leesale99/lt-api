# Executable specification — front door

`docs/spec/` is lt-api's executable specification. The catalog says what every
business rule is and who decides it; the scenarios say what those rules look like from
outside; the checker keeps catalog, code, tests and scenarios tied to each other; the
audit gate refuses a stale or contradictory spec. This file is the workflow.
`rules.schema.md` is the format authority for the catalog.

Where things live:

```
docs/spec/
├── README.md — this workflow (the front door)
├── rules.yaml — canonical, hand-edited catalog; header is a pointer block, the entries are the law
├── rules.schema.md — format authority (enums §1, fields §2, policies §3)
├── gherkin-style.md — Gherkin authoring style
├── glossary.yaml — hand-edited per-module step-vocabulary allowlist (policy 13)
├── business-rules.md — GENERATED dashboard from rules.yaml: totals, per-module counts and the index into views/; never edit
├── views/<module>.md — GENERATED per-module views (game, identity, kernel, ledger, platform, schedule), the full rule blocks; never edit
└── features/ — the Gherkin tree, one capability per file
```

## 1. The pieces

- **Catalog.** `docs/spec/rules.yaml` is the canonical, hand-edited catalog. The readable
  view is **generated** from it: `docs/spec/business-rules.md` is the dashboard (totals,
  the per-module counts, the index) and `docs/spec/views/<module>.md` holds each module's
  full rule blocks — never hand-edit either; only the YAML feeds them.
- **Scenarios.** `docs/spec/features/<module>/<capability>.feature` holds one capability
  of business-visible behaviour per file. Every scenario carries exactly one
  `@rule:R-...` tag. Authoring rules: `gherkin-style.md`.
- **Glossary.** `docs/spec/glossary.yaml` is the per-module step-vocabulary
  allowlist: every word a step uses must be declared in its module's section (or in
  `common`), and policy 13 denies undeclared vocabulary as well as transport/SQL
  words. A new scenario word is a deliberate glossary edit.
- **Checker.** `cmd/spec`: `gen` regenerates the Markdown — the dashboard and every
  module view (or `--diff` to detect staleness in either, and in the `views/`
  directory as a whole: a file the run does not render is an orphan and fails the
  diff); `check` validates the
  catalog's schema policies and traces every site, test pointer and Gherkin pointer to
  something that exists, in both catalog → tree and tree → catalog directions. The
  policies are `rules.schema.md` §3: enums and
  pointers (1–12), the step vocabulary (13), the category's verification/refusal
  binding (14), the `//rule:` annotations on Go enforcement sites (15) and
  `knowledge-ref` (16). `--strict` makes any check error fatal.
- **Enforcement suite.** `internal/spec` is a godog suite run as a plain `go test`
  against real Postgres — real migrations, role-restricted pools, the product's own
  entry points, plus sabotage steps for database-authority proofs. Scenarios tagged
  `@known-defect:<n>` are excluded by default; `SPEC_KNOWN_DEFECTS=1` runs them.

## 2. Rule-change protocol

This is the one canonical account of how a rule is added or changed: red first, then
implementation, then green. Nothing lands without the gate, and this recipe is what
`rules.schema.md` §5 points at.

### The gate

1. Change the catalog entry in `docs/spec/rules.yaml` **and** its scenario under
   `docs/spec/features/**` first.
2. Run `make audit`: the gap between the spec and the code is the failure — the
   scenario is red (and so is an untraceable catalog entry, since `spec/check` runs
   `--strict`).
3. Implement the change.
4. Run `make spec/gen` to regenerate the dashboard and the module views.
5. Run `make audit` again; it is done when green.

Never edit the generated Markdown; never add an enforcement site without updating the
catalog entry that names it.

### The recipe

What step 1 of the gate actually means, for a brand-new rule:

1. **Find the authority.** Ask what kind of rule it is — the kind-of-rule table in
   `rules.schema.md` §1 names the default authority for each. Write the `statement` in
   business language; if it needs code-speak, it is probably two rules.
2. **Read the code, not the docs, for `enforcement`.** Resolve every site against the
   tree today (`grep` the symbol, `ls` the migration). Migration headers and old
   comments are the most common source of stale pointers — verify, then record what you
   verified.
3. **Decide each site's `role`.** One authority layer; everything else is a copy that
   needs `pinned_by` or a loud-refusal `Tie:` note in `notes`.
4. **Name the refusal.** SQLSTATE → constraint/raise name → sentinel → HTTP status. If
   the refusal is unmapped, record `sentinel: null` and say so in `notes`.
5. **Record the knowledge reference when one exists.** When a rule has a
   knowledge-base note behind it under `docs/league-tokens-learning-phases/`, record
   `knowledge-ref:` beside `expert-ref`. Policy 16 validates that the path exists
   under that tree; `knowledge-ref` is additional to `expert-ref`, never a
   replacement.
6. **Point at tests that exist.** Grep for the test before writing the pointer. If none
   exists, the rule is `drifted` (or `implemented` with the gap in `notes` when it is a
   coverage gap, not a disagreement).
7. **Assign the next id** for that module; never reuse or renumber.
8. **Declare the new scenario vocabulary.** Every word a new step uses must be
   declared in the module's section of `docs/spec/glossary.yaml` (or in `common`) —
   policy 13 denies undeclared step vocabulary, so the glossary edit belongs in the
   same change as the scenario.
9. **Annotate new enforcement sites.** When adding an enforcement site to the
   catalog, place a `//rule:R-XXX-NNN` comment on or above the Go declaration (the
   contiguous comment block is fine, one line per id). Policy 15 checks both
   directions: the site must carry its rule, and every annotation must name a
   catalog rule.
10. **Validate the YAML parses.** Stock macOS `python3` has no `yaml` module, so Ruby's
   stdlib `psych` is the reliable one:
   ```sh
   ruby -ryaml -e 'p YAML.safe_load(File.read("docs/spec/rules.yaml")).size'
   python3 -c "import yaml; yaml.safe_load(open('docs/spec/rules.yaml'))"
   ```
11. **Run the gate** (steps 2–5 above). `make spec/check` enforces the
    `rules.schema.md` §3 policies mechanically; then `make spec/gen`; finish when
    `make audit` is green.

## 3. Running each piece

| target | what it does |
|---|---|
| `make spec/gen` | regenerate `docs/spec/business-rules.md` (the dashboard) and `docs/spec/views/<module>.md` (the six module views) from `rules.yaml` |
| `make spec/check` | validate schema policies + catalog/feature traceability; runs `--strict`, so any check error fails the target (and therefore `make audit`) |
| `make spec/diff` | fail if the dashboard or any module view is stale, or if `views/` holds a file the run does not render (an orphan; `gen` reports it but never deletes it) |
| `make spec/brief args='R-GAME-001'` | print a Markdown work order for one rule — statement, sites, refusal map, fixture, resolved verification pointers, linked scenarios, gate — or `args='--module game'` for every rule of one module |
| `make db/up` | start the compose test database (CI's postgres:18 service container) and wait until its healthcheck passes |
| `make db/down` | stop it; the named volume survives; `docker compose down -v` drops the base database too |
| `make test/spec` | run the godog scenarios against real Postgres; fails without LT_API_TEST_DSN (bring it up with make db/up); AUDIT_SKIP_DB=1 turns that into a loud skip |
| `make test` | run the full test tree; every database-backed suite fails without LT_API_TEST_DSN (AUDIT_SKIP_DB=1 announces the skip) |
| `make audit` | the single gate: `tidy -diff` → `mod verify` → gofmt → vet → staticcheck → `spec/diff` → `spec/check` → full test tree |

`make db/up` starts the throwaway test database (compose, same credentials and image
as CI's service container) and waits for it to be healthy. A missing
`LT_API_TEST_DSN` now fails every database-backed suite rather than skipping it, so a
green run always includes those scenarios; `AUDIT_SKIP_DB=1` announces the skip and
is the only opt-out. Quickstart: `make db/up` →
`export LT_API_TEST_DSN='postgres://lt_test:test@localhost:5433/lt_api_test?sslmode=disable'`
→ `make test/spec` (the first run applies migrations 000001–000022 to a database it
creates and drops itself) → `make db/down` when done.

## 4. Agent quickstart

**Picking up a task.** Find the rule in `docs/spec/rules.yaml`; read its `statement`,
`enforcement`, `verification` and `notes`, then open the feature file it points at.
Implement against the behavior those two describe. Run `make audit`; done when green.
If the behavior must change, change the catalog and scenario first (§2).

**Adding a rule.** Follow §2 — the recipe is canonical there. Pick the authority and id,
verify every site and test against the code today, add the catalog entry and its `@rule`
scenario, run `make spec/gen`, then `make audit`.

**Adding a scenario to an existing rule.** One scenario, one rule; name it so
`<feature-file>::<scenario name>` is the pointer in `verification.gherkin`. Reuse
`gherkin-style.md` vocabulary; no transport or SQL words in facade steps.
