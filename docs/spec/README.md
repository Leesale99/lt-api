# The executable specification

`docs/spec/` is lt-api's executable specification. It exists so that every business
rule answers three questions in one place:

1. **What kind of rule is it**, and therefore **who is the single authority** that
   decides it.
2. **Where is it enforced today** — the sites, and whether each site is the authority
   or a copy that is tied back to it.
3. **How do we know it still holds** — the tests that exist *today* and the scenario
   that shows the rule from outside.

Five artifacts answer those questions, and each has one owner:

- **The catalog** — `docs/spec/rules/`, one authored Markdown file per rule. This is
  the source of truth. Never generated.
- **The policies** — `docs/spec/policies.md`, the catalog's own machine-checked
  invariants: what `cmd/spec check` refuses, one section per policy, with the reasoning
  behind it.
- **The scenarios** — `docs/spec/features/`, one capability per file, each scenario
  tagged with exactly one rule id.
- **The checker** — `cmd/spec`, which validates the catalog against its policies and
  traces every pointer into the tree in both directions.
- **The dashboard** — `docs/spec/business-rules.md`, a thin generated summary. Never
  edited by hand.

Three documents hold the format and the style: [`rules.schema.md`](rules.schema.md) is
the format authority — §1 the frozen enums, §2 the rule file, §4 fixtures, §5 how to add
a rule; [`policies.md`](policies.md) states the 18 policies the checker enforces; and
[`gherkin-style.md`](gherkin-style.md) is the Gherkin authoring style. This document is
the workflow around them; the delegate side of the loop is
[`../delegate-workflow.md`](../delegate-workflow.md) and `WORKFLOW.md`.

```
docs/spec/
├── README.md — this workflow
├── rules/ — the catalog: <module>/<ID>.md, one authored file per rule;
│            rules/README.md is the directory's own map
├── rules.schema.md — format authority (enums §1, the rule file §2, adding a rule §5)
├── policies.md — the 18 machine-checked catalog policies, one section each
├── features/ — the Gherkin tree, one capability per file
├── glossary.yaml — the per-module step-vocabulary allowlist (policy 13)
├── prove/ — the registry, one <module>.yaml per module: which scenario
│            attempts each rule's bypass; prove/README.md is the directory's map
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

### What step 1 means

The recipe is [`rules.schema.md` §5 (how to add a rule)](rules.schema.md#5-how-to-add-a-rule),
and it branches once. **Path A — specify first**, the default, authors the rule as
`specified-only` with its expert ref at the domain source, no enforcement and no
verification, plus its `@rule:<ID>` scenario, and shows the batch red; the code, the
verified sites and the flip to `implemented` are a follow-up pass. **Path B — catalog
existing behavior** fills the front matter from the code — one authority, the enforcement
sites verified today, the refusal chain, the verification pointers — and is green
because that code exists. Each path is runnable alone and ends at the same gate.

The recipe also owns the working detail: the module and the next free id (flat per
module, stable once assigned, never reused), the front-matter field table
([`rules.schema.md` §2 (the rule file)](rules.schema.md#2-the-rule-file)), the glossary
words a new step needs — [policy 13 (step text stays inside the
vocabulary)](policies.md#policy-13--step-text-stays-inside-the-vocabulary) — and the
`//rule:` annotation — [policy 15 (every Go enforcement site carries its
annotation)](policies.md#policy-15--every-go-enforcement-site-carries-its-annotation).

## 2. Authoring a rule file

The rule file's shape has one home, and this document does not restate it.
[`rules.schema.md` §2 (the rule file)](rules.schema.md#2-the-rule-file) states the
front-matter field table, the body and its single `## Notes` heading, the loader's
strictness and the refusal-mapping house convention; [`rules.schema.md` §5 (how to add a
rule)](rules.schema.md#5-how-to-add-a-rule) states the two-path recipe — specify first,
or catalog existing behavior — and the gate it ends at; §1 (the frozen enums) of the same
file states the values those fields take.

Format lives there on purpose: a second copy of it here would be a copy that can
disagree.

## 3. The policy registry

The catalog's own invariants. `cmd/spec check` verifies all of them; the ids below match
`cmd/spec/policies.go`, and each policy is stated with its reasoning in
[`policies.md`](policies.md), one section per policy under the same number. An ERROR
fails `--strict`; a WARN never does.

| # | What it holds | Owner |
| --- | --- | --- |
| [1](policies.md#policy-1--id-format-uniqueness-and-flat-per-module-numbering) | the id matches `^R-[A-Z]+-\d{3}$` and is unique, flat per module | catalog |
| [2](policies.md#policy-2--exactly-one-category-status-and-authority) | exactly one `category`, one `status`, one `authority` | catalog |
| [3](policies.md#policy-3--implemented-rules-point-at-sites-and-verification-that-resolve-today) | `implemented` ⇒ at least one enforcement site and one verification pointer, and every pointer resolves today | catalog |
| [4](policies.md#policy-4--specified-only-rules-carry-an-expert-ref-and-nothing-else) | `specified-only` ⇒ `expert-ref`, no enforcement, no verification, no invented refusal | catalog |
| [5](policies.md#policy-5--drifted-rules-carry-notes-stale-pointers-only-on-implemented-rules) | `drifted` ⇒ notes naming the disagreement; `stale_pointer` only on `implemented` | catalog |
| [6](policies.md#policy-6--database-authority-requires-a-mechanism-and-a-write-scope) | `database` authority ⇒ `mechanism` and `write_scope`; any other authority ⇒ no `mechanism` | catalog |
| [7](policies.md#policy-7--non-authoritative-sites-are-tied-by-a-fixture-or-a-tie-note) | a non-authoritative site carries `pinned_by` or a `tie` | catalog |
| [8](policies.md#policy-8--a-deviating-authority-carries-notes) | an authority that deviates from its category default carries notes | catalog |
| [9](policies.md#policy-9--sites-and-pointers-are-pathsymbol-never-line-numbers) | sites and pointers are `path::Symbol`, never line numbers | catalog |
| [10](policies.md#policy-10--every-verificationgherkin-pointer-resolves-to-a-real-scenario) | every `verification.gherkin` pointer resolves to a real scenario | catalog ↔ features |
| [11](policies.md#policy-11--every-pinned_by-names-a-declared-fixture) | every `pinned_by` names a fixture some rule declares | catalog |
| [12](policies.md#policy-12--the-feature-tree-and-the-catalog-agree-in-both-directions) | the feature tree and the catalog agree in both directions, one `@rule` tag per scenario | catalog ↔ features |
| [13](policies.md#policy-13--step-text-stays-inside-the-vocabulary) | every step word is in the glossary, and no step uses transport or SQL vocabulary | `features/**`, `glossary.yaml` |
| [14](policies.md#policy-14--the-category-binds-the-verification-and-refusal-surface) | the category binds the verification and refusal surface of an implemented rule | catalog |
| [15](policies.md#policy-15--every-go-enforcement-site-carries-its-annotation) | every Go enforcement site carries its `//rule:` annotation, and every annotation names a catalog rule | catalog ↔ Go tree |
| [16](policies.md#policy-16--knowledge-ref-exists-under-the-learning-phases) | `knowledge-ref` exists and sits under `docs/league-tokens-learning-phases/` | catalog |
| [17](policies.md#policy-17--the-refusal-code-is-sane) | `refusal.code` is a SQLSTATE or `synthetic`, and `refusal.http` is a real status | catalog |
| [18](policies.md#policy-18--a-scenario-asserts-something-an-observer-can-see) | a scenario asserts something an observer can see: no vacuous Then, category-bound oracles, no expectation copied from the implementation | `features/**` |

Two policies deserve a sentence beyond the table, because they are the ones that
surprise authors:

**[Policy 13 (step text stays inside the
vocabulary)](policies.md#policy-13--step-text-stays-inside-the-vocabulary) is
bidirectional.** A word must be declared to be allowed, *and* some words are denied no
matter what the glossary says: transport vocabulary and bare HTTP status figures in any
step, SQL keywords in facade steps. Inflections are ordinary words
(`deletes`, `created` trigger nothing on their own), so they need a glossary entry like
any other. A database object name's fragments are still scanned, which is why a
sabotage step may only use a constraint or sentinel name that really exists.

**[Policy 18 (a scenario asserts something an observer can
see)](policies.md#policy-18--a-scenario-asserts-something-an-observer-can-see) is what
makes a scenario evidence.** A Then that only reports the code did not explode passes
after the rule was deleted, so it is an error. Nor may a Then repeat a literal verbatim
from the implementation's function bodies — that expectation can no longer disagree with
the code.

The `//rule:` annotation — [policy 15 (every Go enforcement site carries its
annotation)](policies.md#policy-15--every-go-enforcement-site-carries-its-annotation) —
is the one comment convention the tree has: a line that is exactly `//rule:R-XXX-NNN`, on
the declaration or in the contiguous comment block directly above it, one id per line
when a site serves several rules. `gofmt`
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

`make spec/check` runs `go run ./cmd/spec check --strict`: the 18 policies stated in
[`policies.md`](policies.md), every pointer traced into the tree in both directions, and
`--strict` making any ERROR fatal. That is why `make audit/static` cannot pass with a
spec that disagrees with the code, and why an untraceable new entry fails the gate the
moment it is written.

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

The registry is per module at `docs/spec/prove/<module>.yaml`: each file maps a
module's rule ids to an entry carrying the executable scenarios that attempt the
bypass, or a git-attributed human signoff when no executable scenario exists.
Entries are added on demand: a rule enters prove only when a change touches it,
and then a human must either map a scenario or sign an unvalidatable entry. Prove
derives the rules a change touches (committed `base...HEAD` plus the dirty tree, so
an uncommitted change is still proven) — editing one module's registry scopes that
module's entries — or takes an explicit `--rule <id>` / `--all`, resolves every
scenario pointer against the parsed feature tree, rejects every mapping that is
vacuous on inspection, then runs only the mapped godog scenarios (through
`SPEC_FEATURE_PATHS`, so the suite visits those scenarios and no others).

An entry is `scenarios` (one or more `<feature-file>::<scenario name>` pointers,
required for a verifiable entry), plus optionally `signoff` (only for an
UNVALIDATABLE rule) and free-text `notes`. The registry is seeded with the 30
database-authority rules; most map to an Owner-pool write/refusal scenario, while
R-IDENTITY-011 and R-IDENTITY-014 have no sabotage scenario and map to their own
executable proof, saying so in `notes`. `signoff` must carry a git-attributed
identity and the commit that reviewed the exception: a signed UNVALIDATABLE rule
passes, an unsigned one fails.

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
all — it is a merge-back gate (§9, delegated work).

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
plus VERIFIED prove before a merge. If the behaviour itself changes, start at §1 (the
rule-change protocol).

**Adding a rule.** Follow the recipe in [`rules.schema.md` §5 (how to add a
rule)](rules.schema.md#5-how-to-add-a-rule) and take the path that matches your
situation: **Path A — specify first** (the default) authors the rule as `specified-only`
with its `@rule` scenario and leaves the code for a follow-up pass; **Path B — catalog
existing behavior** records the sites and tests the code has today. Both paths end at
the same gate: the scenario and the glossary words in the same change, `make spec/gen`
for the dashboard, `make audit` green, plus VERIFIED prove before a merge (§1).

**Adding a scenario to an existing rule.** One scenario, one rule; name it so that
`<feature-file>::<scenario name>` is the pointer in `verification.gherkin`. Reuse
`gherkin-style.md` vocabulary, and keep transport and SQL words out of facade steps.
