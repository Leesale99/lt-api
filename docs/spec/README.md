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
├── rules/ — the canonical, hand-edited catalog: one authored Markdown file per rule at
│            rules/<module>/<ID>.md (YAML front matter = machine fields, body = notes prose);
│            rules/README.md is the directory's own map
├── rules.schema.md — format authority (enums §1, the rule file §2, policies §3)
├── gherkin-style.md — Gherkin authoring style
├── glossary.yaml — hand-edited per-module step-vocabulary allowlist (policy 13)
├── business-rules.md — GENERATED thin dashboard over rules/: totals by module and
│                       status, plus links into rules/<module>/; never edit
└── features/ — the Gherkin tree, one capability per file
```

## 1. The pieces

- **Catalog.** `docs/spec/rules/` is the canonical, hand-edited catalog: one authored
  Markdown file per rule, `<module>/<ID>.md`, whose YAML front matter carries the machine
  fields (statement, authority, enforcement sites, refusal, verification pointers — see
  `rules.schema.md` §2) and whose body's `## Notes` section carries the rule's notes. The
  file stem is the id and the directory is the module; the loader enforces both. The one
  readable view is **generated**: `docs/spec/business-rules.md` is a thin dashboard —
  totals by module and status plus a link into each `rules/<module>/` — never hand-edit
  it. There is no per-module view file any more: the rule file *is* the block.
- **Scenarios.** `docs/spec/features/<module>/<capability>.feature` holds one capability
  of business-visible behaviour per file. Every scenario carries exactly one
  `@rule:R-...` tag. Authoring rules: `gherkin-style.md`.
- **Glossary.** `docs/spec/glossary.yaml` is the per-module step-vocabulary
  allowlist: every word a step uses must be declared in its module's section (or in
  `common`), and policy 13 denies undeclared vocabulary as well as transport/SQL
  words. A new scenario word is a deliberate glossary edit.
- **Checker.** `cmd/spec`: `gen` regenerates the one generated artifact — the thin
  dashboard (or `--diff` to detect its staleness); `check` validates the catalog's schema
  policies and traces every site, test pointer and Gherkin pointer to something that
  exists, in both catalog → tree and tree → catalog directions. The policies are
  `rules.schema.md` §3: enums and pointers (1–12), the step vocabulary (13), the
  category's verification/refusal binding (14), the `//rule:` annotations on Go
  enforcement sites (15), `knowledge-ref` (16), refusal-code sanity (17) and the
  scenario oracle (18). `--strict`
  makes any check error fatal.
- **Enforcement suite.** `internal/spec` is a godog suite run as a plain `go test`
  against real Postgres — real migrations, role-restricted pools, the product's own
  entry points, plus sabotage steps for database-authority proofs.

## 2. Rule-change protocol

This is the one canonical account of how a rule is added or changed: red first, then
implementation, then green. Nothing lands without the gate, and this recipe is what
`rules.schema.md` §5 points at.

### The gate

1. Change the rule's file under `docs/spec/rules/<module>/` **and** its scenario under
   `docs/spec/features/**` first.
2. Run `make audit`: the gap between the spec and the code is the failure — the
   scenario is red (and so is an untraceable catalog entry, since `spec/check` runs
   `--strict`).
3. Implement the change.
4. Run `make spec/gen` to regenerate the dashboard.
5. Run `make audit` again; it is done when green.

Never edit the generated Markdown; never add an enforcement site without updating the
rule file that names it.

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
   needs `pinned_by` or a loud-refusal `tie:` note in the front matter.
4. **Name the refusal.** SQLSTATE → constraint/raise name → sentinel → HTTP status. If
   the refusal is unmapped, record `sentinel: null` and say so in the `## Notes` body.
5. **Record the knowledge reference when one exists.** When a rule has a
   knowledge-base note behind it under `docs/league-tokens-learning-phases/`, record
   `knowledge-ref:` beside `expert-ref`. Policy 16 validates that the path exists
   under that tree; `knowledge-ref` is additional to `expert-ref`, never a
   replacement.
6. **Point at tests that exist.** Grep for the test before writing the pointer. If none
   exists, the rule is `drifted` (or `implemented` with the gap in `## Notes` when it is a
   coverage gap, not a disagreement).
7. **Assign the next id** for that module — a new file at
   `docs/spec/rules/<module>/<ID>.md`; never reuse or renumber.
8. **Declare the new scenario vocabulary.** Every word a new step uses must be
   declared in the module's section of `docs/spec/glossary.yaml` (or in `common`) —
   policy 13 denies undeclared step vocabulary, so the glossary edit belongs in the
   same change as the scenario.
9. **Annotate new enforcement sites.** When adding an enforcement site to the
   catalog, place a `//rule:R-XXX-NNN` comment on or above the Go declaration (the
   contiguous comment block is fine, one line per id). Policy 15 checks both
   directions: the site must carry its rule, and every annotation must name a
   catalog rule.
10. **Validate the front matter decodes.** Each file's front matter is a small,
    strictly-decoded YAML document, so an unknown or misspelled key fails the load and
    names the file — `make spec/check` is that check and the one to trust. A quick parse
    of the files you touched (stock macOS `python3` has no `yaml` module, so Ruby's
    stdlib `psych` is the portable one):
    ```sh
    ruby -ryaml -e 'ARGV.each { |f| YAML.safe_load(File.read(f)[/\A---\n(.*?)^---$/m, 1]) }' \
      docs/spec/rules/<module>/<ID>.md
    ```
11. **Run the gate** (§2's gate steps 2–5, above). `make spec/check` enforces the
    `rules.schema.md` §3 policies mechanically; then `make spec/gen`; finish when
    `make audit` is green.

## 3. Running each piece

Every `cmd/spec` subcommand takes `--rules`, which defaults to `docs/spec/rules` (the
catalog directory); the targets below are thin `go run ./cmd/spec …` wrappers, so
`go run ./cmd/spec gen --rules docs/spec/rules --out docs/spec/business-rules.md` and
`make spec/gen` are the same run. Scoping: `check` takes `--module <m>` and
`--rule <id>`; `work` takes an optional rule id as a positional argument (one rule's
work order) or `--module <m>` (the queue narrowed to one module); `prove` takes
`--rule <id>`, `--all` and `--base <git-ref>`.

| target | what it does |
|---|---|
| `make spec/gen` | regenerate `docs/spec/business-rules.md` — the thin dashboard (totals by module and status, plus links into `rules/<module>/`) — from the rule files; the rule files themselves are authored, never generated |
| `make spec/check` | validate schema policies + catalog/feature traceability; runs `--strict`, so any check error fails the target (and therefore `make audit`) |
| `make spec/diff` | fail if the generated dashboard is stale (wired into `make audit`) |
| `make prove args='--rule R-GAME-001'` | run the DB-backed mutant registry for the rules a change touches (or `args='--all'`, the default with no args) — not part of `make audit`; needs `LT_API_TEST_DSN` |
| `make spec/work` | print the work queue in priority order — drifted, specified-only, then implemented rules with an enforcement site or verification pointer that does not resolve today; `args='--module game'` narrows it to one module |
| `make spec/work args='R-GAME-001'` | print one rule's work order — the overlay only, since the statement and the notes body live in `docs/spec/rules/<module>/<ID>.md`: field table, sites with a resolution column, refusal map, fixture, resolved verification pointers, linked scenarios, gate |
| `make db/up` | start the compose test database (CI's postgres:18 service container) and wait until its healthcheck passes |
| `make db/down` | stop it; the named volume survives; `docker compose down -v` drops the base database too |
| `make test/spec` | run the godog scenarios against real Postgres; fails without LT_API_TEST_DSN (bring it up with make db/up); AUDIT_SKIP_DB=1 turns that into a loud skip |
| `make test` | run the full test tree; every database-backed suite fails without LT_API_TEST_DSN (AUDIT_SKIP_DB=1 announces the skip) |
| `make audit` | the single gate: `tidy -diff` → `mod verify` → gofmt → vet → staticcheck → `spec/diff` → `spec/check` → full test tree |

`make db/up` starts the throwaway test database (compose, same credentials and image
as CI's service container) and waits for it to be healthy. A missing
`LT_API_TEST_DSN` now fails every database-backed suite rather than skipping it, so a
green run always includes those scenarios; `AUDIT_SKIP_DB=1` announces the skip and
is the only opt-out. `make audit` never trusts the go test cache: it re-executes the
whole tree with `-count=1` on every run, so a cached `ok` can never stand in for the
enforcement half; the other `go test` targets (`make test`, `make test/unit`,
`make test/db`, `make test/spec`) pass `-count=1` for the same reason. Quickstart: `make db/up` →
`export LT_API_TEST_DSN='postgres://lt_test:test@localhost:5433/lt_api_test?sslmode=disable'`
→ `make test/spec` (the first run applies migrations 000001–000022 to a database it
creates and drops itself) → `make db/down` when done.

`cmd/spec` no longer has a `migrate` subcommand: the retired single-file `rules.yaml`
was converted once into `rules/<module>/<ID>.md`, and that one-shot converter is gone
the way the old catalog is. The rule files are the authored format — author and edit
them directly (the writer's parse/render fidelity is pinned by
`cmd/spec/rulefile_test.go` against the live catalog).

## 4. Agent quickstart

**Turn-start ritual.** Before picking anything up, run `make spec/work` and take the
top row — drifted rules first, then specified-only ones, then implemented rules whose
enforcement site or verification pointer does not resolve today. Run
`make spec/work args='<id>'` and use that work order as the task packet; add
the matching `SPEC-SCOPE:` line from §5 so the worker's gates stay scoped. The queue
is a cheap triage view, not a verdict: policies 13, 15 and 18 still only run in
`make spec/check`.

**Picking up a task.** Find the rule's file — `docs/spec/rules/<module>/<ID>.md` (the id
is the file stem); read its front matter (`statement`, `enforcement`, `verification`) and
its `## Notes` body, then open the feature file the `verification.gherkin` pointers name.
Implement against the behavior those two describe. Run `make audit`; done when green.
If the behavior must change, change the catalog and scenario first (§2). The
write boundaries that bind this loop — the worktree mount, the inert in-VM
extension, and the host-only receipts — are in §5.

**Adding a rule.** Follow §2 — the recipe is canonical there. Pick the authority and id,
verify every site and test against the code today, add the rule file and its `@rule`
scenario, run `make spec/gen`, then `make audit`.

**Adding a scenario to an existing rule.** One scenario, one rule; name it so
`<feature-file>::<scenario name>` is the pointer in `verification.gherkin`. Reuse
`gherkin-style.md` vocabulary; no transport or SQL words in facade steps.

## 5. Delegated work: the two planes and the gates

A delegated task is shaped by `.pi/prompts/implement.md`, executed under
`.pi/agents/go-worker.md` (the loop and report format) and driven by the main session
(persona: `.pi/agents/main.md`) and its child orchestrators, against the protocol
those files carry (worktree, commits, approval, merge-back).
`WORKFLOW.md` carries the machine-checked policy: the write boundary, the gate
planes, the integrity check, receipts and claims, and the RED/GREEN/PROVE ladder.
Sandbox profiles — mounts, egress, resources — are declared in
`.pi/extensions/delegate/sbx.ts`. Those files are authoritative; this section is the
map between them and never restates them.

**Two planes.** A worker runs inside a per-task Docker Sandbox micro-VM (`sbx`),
with one mount: the worktree, read-write, at the same absolute path (`scout` gets it
read-only; `librarian` gets the vault read-write and the repo read-only;
`researcher` gets no mount at all). Egress is default-deny per profile, the SSH
agent is not forwarded, and no DB credential is ever exported. The in-VM extension
instance sees `PI_DELEGATE_SANDBOX=1` and is inert: it registers no gate, issues no
verdict, writes no receipt. The host — main session and child orchestrators — is the
verifier plane, but it no longer executes worktree code: every authoritative gate
runs inside a second, locked-down gate VM (`pi-gate:v1`), and the host's execution
surface for the tree is `git` (the integrity check — a trusted host binary reading
paths) and `sbx exec` into that VM. The verdict is the host's: it consumes the
gate VM's exit codes, and receipts, claims, commits and merges stay host-only.

**The write boundary is the hypervisor and the mount.** No path classifier sits in
the agent: outside the mount there is no filesystem, no host process table, no SSH
agent. Three consequences shape the spec loop:

- *Workers are git-less.* `.git` is unresolvable through the mount — no status,
  diff, commit, stash, checkout or branch. The host owns every ref; a worker reports
  a change set, it never names a commit.
- *Workers are DSN-less.* `make audit`, `make prove`, `make test/**` and
  `make test/spec` need `LT_API_TEST_DSN` and cannot run in a VM even if typed.
- *Workers are vault-less*, except the librarian profile — that is a mount, not a
  special case in the prompt.

**RED is host-authored, before the task begins.** The rule file under
`docs/spec/rules/<module>/` and its `@rule:R-...` scenario under
`docs/spec/features/**` are written and committed on `delegate/<job-id>` on the host
plane, and the batch is shown red *for the right reason* there: the new scenario
fails and `spec/check` names the untraceable entry. Only then is a task text
written. A missing or unjustified RED is a stop, not a task. The worker never
authors RED and never edits the scenario into agreement with its code — if the
scenario looks wrong, that is a report, not an edit.

**GREEN-first: the worker loop, against advisory gates.** Implement; add the
`//rule:` annotations and the verification pointers the catalog entry needs. The
in-VM loop is the scoped `go run ./cmd/spec check --strict --module <m>` (or
`--rule <id>`), plus `gofmt` on the touched files, plus `make spec/gen` when the
catalog changed. `make audit/static` is git-aware — its gofmt step enumerates
`git ls-files` — so it is a host-side gate in practice: a worker that hits that
wall reports the exact failing line and keeps iterating on the scoped check; it
does not edit the Makefile and does not invent an `ls-files` substitute. Each of
these runs is **advisory** — it shortens the loop, it does not certify it. Budget:
3 attempts per phase, then a stop and a report; never a silent retry.

Handoff mechanics that still apply, from `.pi/prompts/implement.md`: paste
`make spec/work args='<id>'` output as the task packet (sites, refusal
map, verification pointers, linked scenarios); carry exactly one machine-readable
`SPEC-SCOPE: module=<m>` or `SPEC-SCOPE: rules=R-...,R-...` line — the host
extension parses it to scope the worker's advisory gates, and without it they fall
back to the full spec check; name the owned paths; state the acceptance commands
split by plane; state the budget.

**After exit, the authoritative gates run — behind the integrity check.** The host
drives them; the gate VM executes them. In the order the verification ladder in
`.pi/agents/orchestrator.md` declares:

1. **Integrity check first**, before any gate:
   `git diff --exit-code <base> -- Makefile cmd/spec .github WORKFLOW.md`. Dirty →
   the host refuses to run the gates, the batch goes to human review
   (`HANDOFF_BLOCKED`). The gate files are the definition of green, so a worker
   that touched them has invalidated its own verdict.
2. **Authoritative gates**: the scoped spec check, `make audit/static`,
   `make spec/gen` and, for a rule-change batch, the full `make audit` (DB-backed)
   — each executed in the gate VM, whose image carries an apt-baked Postgres on the
   repo's documented port 5433 (the DSN arrives as a make command-line override,
   the only form that survives the Makefile's `-include .envrc`). The host reads the
   exit codes; these verdicts — not the worker's — are the ones that count.
3. **Diff review** against the acceptance criteria, then commit on
   `delegate/<job-id>` (granular, staged by path, never `git add -A`).
4. **PROVE at merge-back.** `make prove args='--rule <id>'` is a merge-back gate
   executed in the gate VM, never a worker step. It is enforced in the merge
   execution path itself: the merge command is blocked when the scoped rules lack
   VERIFIED prove receipts, whatever the approval route. VERIFIED needs nothing
   more; VACUOUS and BROKEN-TEST are hard fails; UNVALIDATABLE is reported and
   stops for human sign-off.

**Receipts and claims are host-only.** The host extension writes them to the
`receipts_dir` and `claims_dir` declared in `WORKFLOW.md`; a fresh worktree carries
neither (both gitignored), so a sandbox never sees them. A worker citing a receipt
is a category error: a worker's report is claims plus command exit codes, and the
orchestrator's report cites the receipts it caused.

**Red-commit approval.** A batch that adds or changes rules still stops mid-job:
the spec-only RED commit is reviewed through an `AWAITING_APPROVAL` block carrying
its `HEAD SHA:` line; `job_approve` binds that exact SHA, and GREEN/PROVE/REFACTOR
continue only while HEAD still matches it — a moved worktree needs a fresh
approval. The main session's edit/write UI confirmation also stays: it is
host-side, it is the human's, and no sandbox policy replaces it.

### Honest residuals

Documented limitations, not hidden ones:

- **In-VM gates are advisory only.** Nothing a worker reports is a verdict; the
  authoritative run happens in the gate VM after the worker's VM is gone, and the
  host records the verdict the gate VM's exit codes produce.
- **The researcher's web search rides a host relay.** Its profile is built and
  mountless — no repo, no host filesystem view — and egress is the model provider,
  `context7.com`, and the host codex-search relay on `localhost:8787`, the only
  process allowed to talk to `chatgpt.com`. The codex OAuth stays host-side, so no
  OpenAI credential ever enters a VM; while the relay is down or the token is
  expired, web search fails closed (503) — a blocked report, not host reach.
- **The librarian has the vault read-write.** It can write the vault; the bound is
  the profile plus the `lt-api-kb` skill's rules, not a deny. A librarian mistake
  there is caught by the human, not by a gate.
- **Hard links can cross out of a mount** on the same filesystem: a link created
  inside the worktree can point at an inode the mount was never meant to expose.
  Nothing here detects that.
- **The integrity check is the net for poisoned gate files** — and only for the
  four paths it names. A worker can still poison a non-gate file in its read-write
  mount: the full audit at the merge boundary is the backstop, and the
  orchestrator is expected to read the diff rather than trust exit codes alone.
- **Gate commands are single commands.** A compound bash command that mixes a
  declared gate with other segments is deliberately not rewritten into the gate VM,
  so do not write compound gate commands.
