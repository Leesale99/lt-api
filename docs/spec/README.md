# The executable specification

`docs/spec/` is lt-api's executable specification. It exists so that every business
rule answers three questions in one place:

1. **What kind of rule is it**, and therefore **who is the single authority** that
   decides it.
2. **Where is it enforced today** — the sites, and whether each site is the authority
   or a copy that is tied back to it.
3. **How do we know it still holds** — the tests that exist *today* and the scenario
   that shows the rule from outside.

Four artifacts answer those questions, and each has one owner:

- **The catalog** — `docs/spec/rules/`, one authored Markdown file per rule. This is
  the source of truth. Never generated.
- **The scenarios** — `docs/spec/features/`, one capability per file, each scenario
  tagged with exactly one rule id.
- **The checker** — `cmd/spec`, which validates the catalog's own policies and traces
  every pointer into the tree in both directions.
- **The dashboard** — `docs/spec/business-rules.md`, a thin generated summary. Never
  edited by hand.

The format authority is [`rules.schema.md`](rules.schema.md): the frozen enums (§1),
the rule file (§2), the policy registry (§3). This document is the workflow around it.
Gherkin authoring style is [`gherkin-style.md`](gherkin-style.md); the delegate side of
the loop is [`../delegate-workflow.md`](../delegate-workflow.md) and `WORKFLOW.md`.

```
docs/spec/
├── README.md — this workflow
├── rules/ — the catalog: <module>/<ID>.md, one authored file per rule;
│            rules/README.md is the directory's own map
├── rules.schema.md — format authority (enums §1, the rule file §2, policies §3)
├── features/ — the Gherkin tree, one capability per file
├── glossary.yaml — the per-module step-vocabulary allowlist (policy 13)
├── prove.yaml — the registry: which scenario attempts each rule's bypass
├── business-rules.md — GENERATED dashboard; never edit
└── gherkin-style.md — Gherkin authoring style
```

## 1. Rule-change protocol

This is the one canonical account of how a rule is added or changed: red first, then
implementation, then green. Nothing lands without the gate.

### The gate

1. Change the rule's file under `docs/spec/rules/<module>/` **and** its scenario under
   `docs/spec/features/**` first. If the change needs new step words, extend
   `glossary.yaml` in the same edit.
2. Run `make audit`: the gap between the spec and the code is the failure — the
   scenario is red, and an untraceable catalog entry is red too, because `spec/check`
   runs `--strict`.
3. Implement the change, with a `//rule:<ID>` annotation on each new Go enforcement
   site.
4. Run `make spec/gen` to regenerate the dashboard.
5. Run `make audit` again; it is done when green.
6. For a rule change, run `make prove args='--rule <ID>'` and get VERIFIED before the
   merge. Prove is not part of `make audit` — it needs Postgres and it is slow — but it
   is the merge-back gate for any batch that names rule ids.

Never edit the generated dashboard; never add an enforcement site without updating the
rule file that names it.

### The recipe

What step 1 means, for a new rule:

1. **Find the authority.** Ask what kind of rule it is — the kind-of-rule table in
   `rules.schema.md` §1 names the default authority for each category. Write the
   `statement` in business language; if it needs code-speak, it is probably two rules.
2. **Read the code, not the docs, for `enforcement`.** Resolve every site against the
   tree today: grep the symbol, list the migration. Migration headers and old comments
   are the most common source of stale pointers — verify, then record what you
   verified.
3. **Decide each site's `role`.** Exactly one `authoritative` layer; every other site
   is a copy that needs a `pinned_by` fixture or a `tie` showing it cannot drift
   silently (`rules.schema.md` §1 copy policy).
4. **Name the refusal.** SQLSTATE → constraint or raise name → sentinel → HTTP status.
   If the refusal is unmapped, record `sentinel: null` and say so under `## Notes`.
5. **Record the knowledge reference when one exists.** A rule with a knowledge-base
   note behind it records `knowledge-ref:` beside `expert-ref`. Policy 16 checks that
   the path exists under `docs/league-tokens-learning-phases/`; it is additional to
   `expert-ref`, never a replacement.
6. **Point at tests that exist.** Grep for the test before writing the pointer. No test
   means the rule is `drifted` — or `implemented` with the coverage gap named under
   `## Notes`, when the code and the rule agree and only the proof is missing.
7. **Assign the next id** for that module: a new file at
   `docs/spec/rules/<module>/<ID>.md`. Ids are flat per module, stable once assigned,
   never reused and never renumbered.
8. **Declare the new scenario vocabulary.** Every word a new step uses must be declared
   in the module's section of `docs/spec/glossary.yaml` (or in `common`) — policy 13
   denies undeclared step vocabulary, so the glossary edit belongs in the same change.
9. **Run the gate** (§1's six steps). `make spec/check` enforces the policies
   mechanically; `make spec/gen` refreshes the dashboard; done when `make audit` is
   green, plus VERIFIED prove before the merge.

## 2. Authoring a rule file

One rule, one file: `docs/spec/rules/<module>/<ID>.md`. The directory names the module
and the file stem names the id, so the path and the entry can never disagree — the
loader refuses a file whose front matter disagrees with its own location.

```markdown
---
id: R-SCHEDULE-025
title: A team kept in the calendar by its matches cannot be deleted
statement: |
  A team that any match refers to cannot be removed. Match history is durable, so the
  calendar's teams are protected by the fixtures that name them, not by a check the
  application remembers to run.
module: schedule
entity: team
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: cross-table
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
refusal:
  code: "23001"
  constraint:
    - matches_home_team_id_fkey
    - matches_away_team_id_fkey
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestTeamDeleteRestricted
  e2e:
    - internal/e2e/teams_test.go::TestDeleteTeamHandler
  gherkin:
    - docs/spec/features/schedule/deletion.feature::A team a fixture names cannot be deleted
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  TeamRepository.Delete's comment still names players, which migration 000016 dropped.
tie: null
---

## Notes

ON DELETE RESTRICT (not NO ACTION) on both sides of matches: the repository's 23001 map to
ErrRecordInUse is advisory, the FK refuses either way.
```

### Front matter

The front matter is the machine state: one YAML document between two `---` fences,
decoded strictly. The full field table, with types and required/optional rules, is
`rules.schema.md` §2 — read it before writing a field you have not used. What matters
more than the list is what each group of fields is *for*:

- **Identity and classification** — `id`, `title`, `statement`, `module`, `entity`,
  `category`, `status`. `category` picks the default authority; `status` decides which
  other fields are legal at all.
- **The decision** — `authority` (exactly one), plus `mechanism` and `write_scope` when
  the authority is `database`. `authority` deviating from its category's default is
  legal and interesting, but policy 8 requires the rule's notes to say why.
- **Enforcement** — one `enforcement` entry per site, each with a `layer`, a `role` and
  a `pinned_by`. Sites are `path::Symbol` (Go) or the migration id (SQL); never line
  numbers, never a bare package.
- **Refusal** — the chain from the database's named refusal down to the status the
  handler writes. Omitted for rules that cannot refuse a write.
- **Verification** — `unit`, `e2e`, `gherkin` pointers, each of which must exist today.
  Any one of the three satisfies policy 3; `interface`, `refusal-mapping` and
  `persistence-fact` rules are forbidden a `gherkin` pointer by policy 14, and
  `invariant`/`policy` rules are warned when they lack one.
- **Provenance** — `expert-ref` (the phase document a rule came from, required for
  `specified-only`) and `knowledge-ref` (the learning note behind it).
- **Rot and ties** — `stale_pointer` and `tie`, the two fields that used to be prose
  conventions inside the notes.

### `write_scope` decides constraint vs trigger

`write_scope` is about *what the write reads*: `row-local` (only the new row — a
`CHECK` can express it), `cross-row` (needs `OLD`, a sibling row or a group aggregate),
`cross-table` (needs another table's facts, which in a modular monolith is usually a
boundary smell). It is required for `database` authority and optional elsewhere, where
it records the predicate's shape — often the reason a rule left the database at all.

The front-matter key is `write_scope:`. `scope:` is not a field: it is a hard decoding
error, because the loader decodes strictly and "almost the right key" is exactly what
strictness is for.

### `stale_pointer` is rot, not drift

A *rotted pointer* — a comment naming a deleted file, a pointer to a test that moved,
enforcement that no longer lives where the docs claim — does not change what the rule
says. The rule stays `implemented` and the rot goes in `stale_pointer`, which policy 5
refuses on any other status. Content that genuinely disagrees is `drifted`, and the
`## Notes` body says what disagrees and which side the catalog believes.

`spec check` owns both verdicts. `spec work` surfaces `drifted` rules as its first
queue priority but never evaluates `stale_pointer` — a queue row is a hint.

### `tie` is how a copy stays a copy

A rule with two enforcement points that are both authorities has zero. The `tie` field
carries the sentence that shows a data-carrying copy cannot drift silently: the wrong
copy is refused at the authority, and the authority's own test fails first. Policy 7
accepts a non-null `tie` in place of a `pinned_by` fixture for a `defense-in-depth`
site; an `advisory-copy` site, which only shapes the refusal, needs neither. A copy
with neither is a second copy of the rule, and the remedy is `status: drifted` with the
missing tie named in the notes.

### The body

Everything after the closing fence is authored prose. The loader reads exactly one
thing out of it: the text under the single `## Notes` heading, which becomes the rule's
notes and is what policies 5 and 8 look at.

- `## Notes` is the conventional home for the reasoning that does not fit a front
  matter field: why this authority, why this ordering, what the residual gap is.
- Further `##` sections are documentation the loader ignores — `## Refusal` is where
  a refusal chain that needs more than the front matter map is spelled out, and
  `## Notes` ends at the next level-2 heading.
- A non-empty body with **no** `## Notes` heading is an error, because that prose would
  otherwise be invisible to the loader. A rule with nothing to add has no body at all.
- `notes` is not a front-matter field; a `notes:` key is a hard decoding error.

### The loader's strictness

The layout is strict because a misplaced file would silently drop a rule:

- Top level: one directory per module, named by a value of the module enum. The single
  exception is `rules/README.md`, authored prose skipped by the loader. Any other
  non-directory entry, dotfile or non-module directory is an error.
- Inside a module directory: rule files only — regular, non-dotfile, `*.md`. Every
  `.md` file there is parsed as a rule.
- Per file: the front matter opens with `---` on line 1, decodes strictly, and its `id`
  and `module` must equal the file stem and the directory name. Ids are unique across
  the whole catalog.
- Ordering is deterministic and independent of readdir order: module enum order, then
  id ascending.

A quick standalone parse of the files you touched, before running the gate (stock macOS
`python3` has no YAML module; Ruby's stdlib does):

```sh
ruby -ryaml -e 'ARGV.each { |f| YAML.safe_load(File.read(f)[/\A---\n(.*?)^---$/m, 1]) }' \
  docs/spec/rules/<module>/<ID>.md
```

## 3. The policy registry

The catalog's own invariants. `cmd/spec check` verifies all of them; the ids below match
`cmd/spec/policies.go`, and the full statements live in `rules.schema.md` §3. An ERROR
fails `--strict`; a WARN never does.

| # | What it holds | Owner |
| --- | --- | --- |
| 1 | the id matches `^R-[A-Z]+-\d{3}$` and is unique, flat per module | catalog |
| 2 | exactly one `category`, one `status`, one `authority` | catalog |
| 3 | `implemented` ⇒ at least one enforcement site and one verification pointer, and every pointer resolves today | catalog |
| 4 | `specified-only` ⇒ `expert-ref`, no enforcement, no verification, no invented refusal | catalog |
| 5 | `drifted` ⇒ notes naming the disagreement; `stale_pointer` only on `implemented` | catalog |
| 6 | `database` authority ⇒ `mechanism` and `write_scope`; any other authority ⇒ no `mechanism` | catalog |
| 7 | a non-authoritative site carries `pinned_by` or a `tie` | catalog |
| 8 | an authority that deviates from its category default carries notes | catalog |
| 9 | sites and pointers are `path::Symbol`, never line numbers | catalog |
| 10 | every `verification.gherkin` pointer resolves to a real scenario | catalog ↔ features |
| 11 | every `pinned_by` names a fixture some rule declares | catalog |
| 12 | the feature tree and the catalog agree in both directions, one `@rule` tag per scenario | catalog ↔ features |
| 13 | every step word is in the glossary, and no step uses transport or SQL vocabulary | `features/**`, `glossary.yaml` |
| 14 | the category binds the verification and refusal surface of an implemented rule | catalog |
| 15 | every Go enforcement site carries its `//rule:` annotation, and every annotation names a catalog rule | catalog ↔ Go tree |
| 16 | `knowledge-ref` exists and sits under `docs/league-tokens-learning-phases/` | catalog |
| 17 | `refusal.code` is a SQLSTATE or `synthetic`, and `refusal.http` is a real status | catalog |
| 18 | a scenario asserts something an observer can see: no vacuous Then, category-bound oracles, no expectation copied from the implementation | `features/**` |

Two policies deserve a sentence beyond the table, because they are the ones that
surprise authors:

**Policy 13 is bidirectional.** A word must be declared to be allowed, *and* some words
are denied no matter what the glossary says: transport vocabulary and bare HTTP status
figures in any step, SQL keywords in facade steps. Inflections are ordinary words
(`deletes`, `created` trigger nothing on their own), so they need a glossary entry like
any other. A database object name's fragments are still scanned, which is why a
sabotage step may only use a constraint or sentinel name that really exists.

**Policy 18 is what makes a scenario evidence.** A Then that only reports the code did
not explode passes after the rule was deleted, so it is an error. Nor may a Then repeat
a literal verbatim from the implementation's function bodies — that expectation can no
longer disagree with the code.

The `//rule:` annotation (policy 15) is the one comment convention the tree has: a line
that is exactly `//rule:R-XXX-NNN`, on the declaration or in the contiguous comment
block directly above it, one id per line when a site serves several rules. `gofmt`
reflows it to `// rule:` — same annotation, same exact id after the colon. A Go
enforcement site without its annotation is an error, and so is an annotation naming a
rule that does not exist.

## 4. `spec work` — the queue and the work order

`spec work` is one command with two faces, because an agent shouldn't have to know which
of two near-identical views to ask for.

**The queue** (`make spec/work`, optionally `args='--module <m>'`) answers "what should I
pick up next?" without running the checker. It reads the catalog and the feature tree
and sorts by *why* a rule is queued: `drifted` rules first, then `specified-only`, then
`implemented` rules with an enforcement site or verification pointer that does not
resolve today.

**The work order** (`make spec/work args='R-XXX-NNN'`) is the per-rule overlay: the
field table, each enforcement site with a resolution column, the refusal map, the
fixture, the resolved verification pointers, the linked scenarios and the gate to run.
It deliberately does not reprint the statement or the notes — those are in
`docs/spec/rules/<module>/<ID>.md`, and the overlay links there instead of becoming a
second copy that can rot.

A queue row is a cheap triage hint, never a verdict. Everything the queue does not
evaluate — step vocabulary, annotations, oracle assertions, ties, stale pointers — is
surfaced as a note pointing at `spec check`, which owns the verdict.

## 5. `spec check --strict`

`make spec/check` runs `go run ./cmd/spec check --strict`: the policy registry above,
every pointer traced into the tree in both directions, and `--strict` making any ERROR
fatal. That is why `make audit/static` cannot pass with a spec that disagrees with the
code, and why an untraceable new entry fails the gate the moment it is written.

Scoping is how a worker — or a focused human run — narrows the report:

- `--module <m>` — the rules that module owns (the scope expands against the catalog).
- `--rule <ID>` — exactly one rule.
- The two are mutually exclusive.

Findings carry a **subject**: the rule id a finding is about, or nothing when the
finding is catalog-level (a broken glossary, a feature file in no module, an annotation
that resolves to no rule). Scoping filters on the subject, which gives three behaviours:
a finding whose subject is in scope is reported at its own severity, so a
rule-attributed vocabulary or annotation error still fails a scoped run; a
catalog-level finding is still printed but demoted to a WARN while scoped, so global rot
is visible without failing a worker's gate over something outside its task; and a
feature-level finding (a broken gherkin pointer, a scenario tagged with an unknown id)
is reported at its natural severity even when its rule is out of scope, because the
feature tree is one artifact and a half-printed cross-validation would read as if the
rest of the tree had been verified.

## 6. `spec/gen` — the one generated artifact

`make spec/gen` writes `docs/spec/business-rules.md` from the rule files: totals by
module and status, plus links into each `rules/<module>/`. That is the whole artifact.
The rule files are authored, and there is no per-module generated view — the rule file
*is* the block.

Byte-identical output for identical input is a hard requirement (no clocks, no map
iteration order), which is what lets `spec/gen --diff` gate freshness. `make spec/diff`
is that check and it is wired into `make audit`, so a hand-edited dashboard or a
catalog change without a regeneration fails the audit.

## 7. Prove

`make prove` is the slow verification gate, and it answers a different question from
`check`: not "is the rule still catalogued consistently?" but "does the rule's
enforcement still refuse the bypass?".

`docs/spec/prove.yaml` is the registry: for each provable rule, the executable
scenarios that attempt its bypass, or a git-attributed human signoff when no executable
scenario exists. Prove derives the rules a change touches (committed `base...HEAD` plus
the dirty tree, so an uncommitted change is still proven), or takes an explicit
`--rule <id>` / `--all`, rejects every mapping that is vacuous on inspection, then runs
only the mapped godog scenarios (through `SPEC_FEATURE_PATHS`, so the suite visits those
scenarios and no others).

The verdict per rule:

| Verdict | Meaning |
| --- | --- |
| `VERIFIED` | the mapped scenarios pass — the attempted bypass was refused as the rule says — nothing more is needed |
| `VACUOUS` | the registry mapping is itself unproven: a pointer resolves to no scenario, or the scenario statically asserts nothing observable (no Then, only a vacuous Then, or a "write is allowed" Then on a rule that claims the database refuses it) |
| `BROKEN-TEST` | the scenario failed while the codebase was intact — the test is wrong, not the rule |
| `UNVALIDATABLE` | no executable scenario mapped and no git-attributed signoff; stops for a human |

VERIFIED is the only verdict that needs nothing more. VACUOUS and BROKEN-TEST are hard
fails; UNVALIDATABLE is reported and stops for sign-off. It needs `LT_API_TEST_DSN`,
which is why it is not part of `make audit` and why it cannot run inside a worker VM at
all — it is a merge-back gate (§9).

## 8. Targets and quickstart

Every subcommand takes `--rules`, defaulting to `docs/spec/rules`. The make targets are
thin wrappers, so `make spec/gen` and
`go run ./cmd/spec gen --rules docs/spec/rules --out docs/spec/business-rules.md` are
the same run.

| target | what it does |
| --- | --- |
| `make spec/gen` | regenerate the dashboard from the rule files |
| `make spec/diff` | fail if the dashboard is stale (part of `make audit/static`) |
| `make spec/check` | validate the policy registry and traceability; `--strict` |
| `make spec/work` | print the queue; `args='<ID>'` for one rule's work order, `args='--module <m>'` to narrow the queue |
| `make prove` | run the mutant registry; `args='--rule <ID>'`, `args='--all'` (the default with no args), `args='--base <ref>'` |
| `make test/spec` | run the godog scenarios against real Postgres (`internal/spec`) |
| `make db/up` / `make db/down` | start / stop the throwaway test database (the image CI uses) |
| `make audit/static` | the DB-free half of the gate: tidy, mod verify, gofmt, vet, staticcheck, spec freshness, spec check |
| `make audit` | the single gate: `audit/static` plus the whole test tree, `-count=1` |

Quickstart for the DB-backed half:

```sh
make db/up
export LT_API_TEST_DSN='postgres://lt_test:test@localhost:5433/lt_api_test?sslmode=disable'
make test/spec      # first run applies the migrations to a database it creates and drops
make db/down
```

A missing `LT_API_TEST_DSN` fails every database-backed suite rather than skipping it,
so a green run always includes those scenarios; `AUDIT_SKIP_DB=1` announces the skip and
is the only opt-out. `make audit` re-executes the whole tree on every run because a
cached `ok` must never stand in for the enforcement half.

## 9. Delegated work: what the spec loop depends on

The full narrative is [`../delegate-workflow.md`](../delegate-workflow.md); the
machine-checked policy is `WORKFLOW.md`. Four facts about that machinery change how the
spec loop feels, and each follows from a property of the worker's sandbox rather than
from a rule about prompts:

- **RED is host-authored.** The rule file and its `@rule:<ID>` scenario are written and
  committed on the host plane before the task text exists, and the batch is shown red
  for the right reason there. A worker never authors RED and never edits a scenario into
  agreement with its code — a scenario that looks wrong is a report.
- **The worker's gates are advisory.** A worker's VM has no usable `.git` (so
  `make audit/static` cannot be its gate) and no `LT_API_TEST_DSN` (so `make audit` and
  `make prove` cannot run at all). Its loop is the scoped
  `spec check --strict --module <m>` / `--rule <id>` plus `gofmt`, plus `make spec/gen`
  when the catalog changed. Those runs shorten the loop and certify nothing.
- **PROVE is a merge-back gate.** It is not a worker step and not a formality: for a
  rule-change batch the merge is blocked mechanically unless the job's scoped rules
  carry VERIFIED prove receipts, however the approval arrived.
- **The scope comes from the packet.** `make spec/work args='<ID>'` output is pasted as
  the task packet, and exactly one `SPEC-SCOPE:` line declares the worker's scope
  (grammar: `WORKFLOW.md` § Task packets, owner: `.pi/extensions/delegate/protocol.ts`).
  That line is what scopes the worker's advisory gates, what the claims log records and
  what the merge-back resolves against — so it is worth one careful line.

Because the worktree is a mount, a worker reports a change set and the host names the
commit; because receipts and claims are host-only paths (both gitignored, absent from a
fresh worktree), a worker citing a receipt is a category error. A worker's report is
claims plus exit codes.

## 10. Agent quickstart

**Turn start.** Run `make spec/work` and take the top row — drifted first, then
specified-only, then broken pointers. Run `make spec/work args='<ID>'` for the work
order and use it as the task packet, with the matching `SPEC-SCOPE:` line from §9.

**Picking up a task.** Open `docs/spec/rules/<module>/<ID>.md` (the id is the file
stem): front matter for the statement, enforcement, refusal and verification; `## Notes`
for the reasoning. Then open the scenario the `verification.gherkin` pointer names.
Implement against what those two describe. `make audit` is the gate; done when green,
plus VERIFIED prove before a merge. If the behaviour itself changes, start at §1.

**Adding a rule.** Follow §1's recipe — pick the authority and the next id, verify every
site and test against the tree today, add the rule file and its `@rule` scenario and the
glossary words, regenerate the dashboard, run the audit.

**Adding a scenario to an existing rule.** One scenario, one rule; name it so that
`<feature-file>::<scenario name>` is the pointer in `verification.gherkin`. Reuse
`gherkin-style.md` vocabulary, and keep transport and SQL words out of facade steps.
