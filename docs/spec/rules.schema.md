# Business-rule catalog — schema

`docs/spec/rules/` is the **canonical, hand-edited** catalog of the project's business rules: one authored
Markdown file per rule at `<module>/<ID>.md` — a strict YAML front matter between two `---` fences holding the
machine fields this document specifies, followed by an optional authored body whose `## Notes` section is the
rule's notes. `docs/spec/coverage.md` is **generated** from those files: a thin coverage dashboard of totals
and links into the rule files. Never edit it.

The catalog exists so that every rule answers three questions in one place:

1. **What kind of rule is it** and therefore **who is the single authority** (the decision owner) for it.
2. **Where is it enforced today** — the sites, and whether each site is the authority or a copy that must be
   tied back to it.
3. **How do we know it still holds** — the tests that exist *today*, and the expert document the rule came
   from.

Four kinds of rule decide where logic lives: an invariant (data integrity) is enforced by the database, a
policy (business math and windows) by the domain, orchestration (order, transaction boundary) by the service,
and an interface rule (request framing) by the handler. Vocabulary follows the vault's lessons: *Four kinds of
rule decide where logic lives*, *Source of truth per invariant*, *A rule with two enforcement points has
zero*, and *Tactical design heuristics*.

> **Naming note.** This document says `repository`; it means the layer some older
> material calls *store* — the Go type is `Repository`.

---

## 1. Frozen enums

### `category` — what kind of rule (decides the default authority)

| value | one-line meaning | default authority |
|---|---|---|
| `invariant` | Must always be true, whoever writes. Unrepairable data if unchecked. | `database` |
| `policy` | Is this allowed / computed correctly *right now* (phase windows, bonus math). | `domain` |
| `orchestration` | Order, transaction boundary, idempotency claim, what must precede what. | `service` |
| `interface` | The request is framed correctly (present, parseable, shape/score arithmetic). | `handler` |
| `persistence-fact` | Store-stamped fact, not a business decision (id, `created_at`, `version`). | `repository` |
| `refusal-mapping` | Which named refusal a violation produces (code → sentinel → status), and in what order causes are classified. | per rule |
| `boundary` | Cross-context hygiene: no cross-context SQL/joins/triggers/FKs; events in the publisher's language, ACL at each edge. | composition root |

The one reading test: *what breaks if nobody checks this rule?* Unrepairable wrong data reachable by any
writer → database. A caller acting before it is allowed, or a formula that changes with the product → domain
(checked **at the service door** for any caller). Ordering/locking/atomicity mechanics → **repository**.
Request framing → handler.

### `authority` — the single decision owner (exactly one per rule)

| value | meaning |
|---|---|
| `database` | A constraint or a trigger refuses the write. |
| `domain` | The domain operation / pure function decides; callers may not bypass it. |
| `service` | The use case decides the order and the transaction boundary. |
| `repository` | The repository executes the atomic write and its write-instant gates. |
| `composition-root` | Cross-context wiring: which facade/ACL each context receives and the direction of every dependency. |
| `handler` | The transport frames and refuses the request. |

`category` names the **default**; `authority` records the **actual** decision owner. A rule whose authority
deviates from its category's default MUST say why in its `## Notes` (policy 8, a deviating authority carries
notes). Deviations are the interesting entries — they are where a rule migrated across a boundary (e.g.
`R-GAME-010` left the database because its trigger read another context's table).

#### Database vs repository authority

Both refuse a forbidden write, but by different means, and the difference decides what proves the rule.

| | `database` | `repository` |
|---|---|---|
| **path dependence** | **Path-independent**: the CHECK / trigger / unique / composite-FK fires on every write from any path — the application, `psql`, a future service, a backfill script. | **Path-dependent**: the predicate, CAS, `FOR SHARE` or guard-function call lives inside specific repository code paths. A *new* query that forgets the guard bypasses the rule silently. |
| **verification** | A sabotage scenario that attempts the forbidden write through the Owner pool, bypassing application code entirely, and asserts the refusal. | Call-set pinning fixtures that prove every writing query in the repository includes the guard, so a new ungated path fails a test. |

Choose `database` whenever the predicate can be stated row-locally without policy: it is the only authority
that survives a writer you have not written yet. Choose `repository` when the predicate needs Go
(cross-context facts, an ordering decision, a computed value) — and pay for it with a call-set fixture,
because the guards are only as good as the set of queries that remember to call them.

### `mechanism` — only when `authority: database`

| value | meaning |
|---|---|
| `constraint` | `CHECK` / `NOT NULL` / `UNIQUE` / `FK` — row-local, declarative, preferred. |
| `trigger` | Only what a constraint cannot say: cross-row reads, `OLD` vs `NEW`, `INSERT`-only predicates, in-tx sequencing. |
| `composite-fk` | Cross-table consistency without procedural code. |

Sub-ordering inside the database: **declarative constraints first**, triggers only for what a constraint
cannot express. Policy never moves into SQL — a pricing formula in PL/pgSQL is a rule nobody can see, test or
rename.

Seeded reference data takes no mechanism value of its own: rows inserted by an applied migration (e.g.
`R-IDENTITY-014`'s roles, permission codes and grants) are catalogued as `mechanism: constraint`, because what
refuses a conflicting seed is the keys and what keeps the seeded values true is migration immutability. No
`seed` / `reference-data` value is added.

### `write_scope` — decides constraint vs trigger

| value | meaning |
|---|---|
| `row-local` | The predicate references only `NEW` — a `CHECK` can express it. |
| `cross-row` | Needs `OLD`, a sibling row, or a group aggregate. |
| `cross-table` | Needs another table's facts. In a modular monolith that is usually a boundary smell — see `boundary`. |

The front-matter key is `write_scope:`. `scope:` is a hard decoding error, because the rule file decodes
strictly (§2, the rule file). The field is about *what the write reads*, never about which module or run the
rule belongs to — that is what `module` and `expert-ref` are for.

### `enforcement[].layer` — where the site lives

| value | meaning |
|---|---|
| `handler` | Transport: DTO validation, status mapping, headers. |
| `composition-root` | Cross-context wiring / facade binding — the composition-root layer where modules are handed their collaborators (a `cmd/...` wiring function, a facade binding, an ACL adapter chosen at assembly). It also names the startup/assembly seam, where a program is composed once at boot (the `main.go` that calls the wiring function, resolves configuration and starts the process) — so a fact about what happens exactly once before anything is served belongs here. Distinguished from `handler`, which is transport framing: nothing here sees a request. |
| `validator` | A `ValidateX` function: request shape/bounds arithmetic, called from the handler before the use case. |
| `service` | Use case: ordering, transaction boundary, idempotency claim. |
| `domain` | Entity/operation or pure function. |
| `repository` | Repository/adapter or the write-instant SQL gate it calls (row locks, `FOR SHARE`, guarded `UPDATE ... WHERE`). |
| `db-constraint` | A declarative constraint in a migration. |
| `db-trigger` | A trigger function in a migration. |

### `enforcement[].role`

| value | meaning |
|---|---|
| `authoritative` | This site **is** the decision, for the rule's one authority layer. Repeated only for disjoint cases (one per command's phase window; total vs delta of one formula). |
| `defense-in-depth` | A data-carrying copy. Trusted only while something ties it to the authority: `pinned_by`, or the rule's `tie:` note showing it cannot drift silently (the authority refuses the wrong copy). |
| `advisory-copy` | Caller-facing pre-check that only improves the refusal shape (a clean 409/422 instead of an error-path 500). The authority still refuses; drift is a UX divergence, never a data one. |

**Copy policy.** Exactly one `authority` layer. A non-authoritative site is allowed only if it names what ties
it to the authority:

- `pinned_by: <fixture>` — a shared change-detector fixture (§4, fixtures), **or**
- `pinned_by: null` **and** the rule's `tie:` field (the rule file, §2) showing the copy cannot drift
  silently: **defense-in-depth** must fail loudly at the authority (wrong copy ⇒ the constraint/trigger
  refuses and its own test fails first); **advisory-copy** is exempt by definition — it only shapes the
  refusal, the authority still refuses the write.

If neither holds for a data-carrying copy, the entry is a second copy of the rule — which the vault counts as
zero. The remedy is `status: drifted` with the missing tie named in the rule's `## Notes`.

### `status`

| value | meaning |
|---|---|
| `implemented` | The rule holds in code today, every named site exists, and at least one verification pointer exists. |
| `specified-only` | Designed from the domain sources; no code yet. `expert-ref` required, no `enforcement`, no `verification`. |
| `drifted` | The rule's **content** disagrees with the domain sources (`domain-knowledge.md`, `domain-knowledge-game-engine.md`) or with actual behavior: the code refuses what the doc allows (or vice versa), the formula/window/ordering differs, or a data-carrying copy has no tie. The `## Notes` body says what disagrees and which side the catalog believes. Notes required. |

**Rot is not drift.** A *rotted pointer* — a change-detector naming a dead file, a comment pointing at a
deleted test, enforcement that moved away from where the docs claim it lives — does NOT make a rule `drifted`:
its content still holds. Keep `status: implemented` and record the rot in the rule's `stale_pointer` field
(the rule file, §2); policy 5 (stale pointers only on implemented rules) refuses that field on any other
status.

Both `drifted` rules and every `stale_pointer` MUST be surfaced by `spec/check`, which owns the verdict —
`spec work` surfaces drifted rules as its first queue priority, but it never evaluates `stale_pointer`.

When a rule conflicts with the domain sources (`domain-knowledge.md`, `domain-knowledge-game-engine.md`), the
domain sources win: the rule is marked `drifted` and fixed; the source is not bent to the code.

Applied migrations are immutable history; when rot is caused by stale prose in an applied migration, the fix
is the catalog (and a comment at the current site), not a rewrite of the migration.

### `module`

`schedule` | `game` | `identity` | `ledger` | `kernel` | `platform`

### Mapping from the coarser enums

The coarse enum values `authority: db|app|both` and `layer: validator|domain|service|db|middleware` map onto
this schema's taxonomy: the five-value authority and six-value layer name *which* app layer. The table below
records the mapping. It is a reading aid, not a transform: a rule file carries this schema's values directly,
and the threadbare dashboard prints no per-rule fields at all.

| coarse | this schema |
|---|---|
| `authority: db` | `database` |
| `authority: app` | `domain` \| `service` \| `repository` \| `composition-root` \| `handler` (the actual value) |
| `authority: both` | not a valid state: one `authority` + N `enforcement` entries |
| `layer: validator` | `validator` (own value; use `handler` for inline DTO checks) |
| `layer: db` | `db-constraint` or `db-trigger` |
| `layer: middleware` | `handler` |

---

## 2. The rule file

A rule is one file: `docs/spec/rules/<module>/<ID>.md`. The directory names the module and the file stem names
the id, so the path and the entry can never disagree. Here is a real one (`schedule/R-SCHEDULE-025.md`),
abridged — shortened prose, fewer pointers:

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

The front matter is the machine state: exactly the fields below, decoded strictly. It is a block of YAML
between two `---` fences at the very top of the file and holds exactly **one** YAML document.

| field | type | required | notes |
|---|---|---|---|
| `id` | string | yes | `R-<MODULE>-NNN`, flat per module, stable once assigned. Must equal the file's stem. Never renumber. A retired rule's file is deleted from the catalog and recorded in `rules/README.md`'s retired-ids note; its id is never reused. |
| `title` | string | yes | Short imperative name ("Rides are born locked"). |
| `statement` | string (block) | yes | 1–3 sentences of business language. What the business demands, no code-speak. It is a **front-matter** field, not body prose: the statement stays machine-readable — greppable and carried by the one strict-decoded document — rather than swallowed into body prose, so no generated view has to parse it back out (ADR-030). |
| `module` | enum | yes | Owner context of the *rule*, not of every enforcement site. Must equal the name of the directory the file sits in (§2, the loader's strictness). |
| `entity` | string | yes | Table or aggregate the rule is about (`ride`, `balance`, `journal_entry`). **Descriptive only** — no policy reads it; it exists for the work-order field table. |
| `category` | enum | yes | The frozen enum, §1. |
| `status` | enum | yes | The frozen enum, §1. |
| `authority` | enum | yes | Exactly one. |
| `mechanism` | enum \| null | iff `authority: database` | The frozen enum, §1. |
| `write_scope` | enum \| null | required for `authority: database`; optional elsewhere | The frozen enum, §1. `scope:` is a hard error. Outside the database it records the predicate's *shape*, which is often the reason the rule left the DB (`R-GAME-010`: `cross-table` is exactly why 000017 moved it to Go). |
| `enforcement` | list | yes (may be empty only for `specified-only`) | Each item: `site`, `layer`, `role`, `pinned_by`. |
| `enforcement[].site` | string | yes | Go: `path::Symbol` (`internal/game/internal/core/domain/ride.go::Ride.Create`). SQL: the migration id (`migrations/000012_add_rides_insert_state_gate.up.sql`). **Never line numbers.** |
| `enforcement[].layer` | enum | yes | The frozen enum, §1. |
| `enforcement[].role` | enum | yes | The frozen enum, §1. |
| `enforcement[].pinned_by` | string \| null | yes (explicit) | Fixture id. `null` is allowed only under the copy policy in §1 (the frozen enums): a `tie:` note for defense-in-depth, nothing needed for advisory-copy. |
| `refusal` | map \| null | recommended | The refusal chain in the refusal-mapping house convention later in this section. Omitted for rules that cannot refuse (pure computation) and for `specified-only` rules whose refusal surface is undesigned. |
| `refusal.code` | string | in refusal | A 5-character SQLSTATE, or the reserved word `synthetic` (a Go-only refusal with no SQLSTATE). **Illustrative, not a closed list:** `P0001`, `23514`, `23503`, `23001` are the codes the house table below shows most often; any real SQLSTATE (`23505`, `23502`, `22003`, `42501`, …) is valid. `cmd/spec` warns — never errors — on a code outside the illustrative set. |
| `refusal.constraint` | string \| list \| null | in refusal | The named refusal (constraint name, or the name the trigger `RAISE`s). A list when a rule carries several constraints. |
| `refusal.sentinel` | string \| null | in refusal | The domain sentinel the repository/ACL maps to (`ErrInvalidTransition`, …). `null` = unmapped. |
| `refusal.http` | int \| null | in refusal | Status the handler writes; must be a real status (100–599). `null` = falls through to 500. |
| `fixture` | string \| null | no | The shared change-detector fixture id this rule's copies are pinned to. Every `pinned_by` in the catalog must name this id or another rule's `fixture`. |
| `verification.unit` | list of strings | yes for `implemented` (one of three) | `path::TestSymbol` (add `::Subtest` text only if the subtest name is stable). One of `unit`/`e2e`/`gherkin`: **any one** satisfies policy 3. Must exist **today** — grep it. |
| `verification.e2e` | list of strings | no | `path::TestSymbol` for existing E2E coverage. Counts toward policy 3 exactly like `unit`; a rule proved only by an e2e test is covered. Must exist **today**. |
| `verification.gherkin` | list of strings | no | `<feature-file>::<scenario name>` under `docs/spec/features/**`; every scenario carries an `@rule:R-...` tag. Counts toward policy 3 like `unit`/`e2e`; policy 10 resolves every pointer against the feature tree. |
| `expert-ref` | string (path) | required for `specified-only` | The domain source the rule comes from — a section of `docs/league-tokens-learning-phases/domain-knowledge.md` or `domain-knowledge-game-engine.md` (policy 4). Existing rules may still point at a phase guide (28 do today); the checker accepts any existing file under `docs/league-tokens-learning-phases/`. Omitted where no doc covers the rule. |
| `knowledge-ref` | string (path) | no | An auxiliary knowledge note behind the rule, relative to the repository root, under `docs/league-tokens-learning-phases/` (policy 16) — typically the phase guide that implements the rule. It is **additional** to `expert-ref`, never a replacement. |
| `stale_pointer` | string (block) \| null | no | A **rot** note: pointers that no longer resolve (a dead file, a deleted test, enforcement that moved). Legal **only** on `status: implemented` — policy 5 refuses it on any other status (`stale-pointer note on a <status> rule`). It is a front-matter field, not a `stale-pointer:` prefix inside `notes`; a prefix in prose is just prose. |
| `tie` | string (block) \| null | no | How a data-carrying copy is tied to its authority: the sentence that shows it cannot drift silently (wrong copy ⇒ the authority refuses, and its own test fails first). Policy 7 accepts a non-null `tie` in place of a `pinned_by` fixture for a `defense-in-depth` site; `advisory-copy` needs neither. It is a front-matter field; a `Tie:` sentence inside `notes` is prose only. |

There is **no `notes` field.** The rule's notes are the prose under the body's `## Notes` heading; `notes:` in
the front matter is a hard decoding error (the loader's strictness, later in this section).

### The body

Everything after the closing `---` fence is authored prose — Markdown, at the author's discretion. What the
loader reads out of it is exactly one thing: the prose under the single `## Notes` heading, which becomes the
rule's notes. That is the section policies 5 and 8 look at (`drifted` ⇒ notes; `authority` ≠ category default
⇒ notes); `spec work <ID>` points at this section rather than reprinting it. Policy 7's tie is **not** read
from here: it is the `tie` front-matter field.

- A body may carry further `##` sections; they are documentation, not machine state, and the loader ignores
  them. Notes end at the next level-2 heading.
- A **non-empty body with no `## Notes` heading is an error** — that prose would otherwise be invisible to the
  loader. A rule with nothing to say has no body at all (the last fence line ends the file).
- `notes`, `stale_pointer` and `tie` are compared in canonical form: every whitespace run collapses to one
  space and leading/trailing whitespace is dropped, so an author's line wrapping can never change the decoded
  value.

### The loader's strictness

The directory layout is strict, because a misplaced file would silently drop a rule:

- Top level: one directory per module, named by a value of the frozen module enum. The single exception is
  `README.md` at the rules-directory top level — authored prose about the layout, skipped by the loader and
  not a rule. Any other non-directory entry is an error, and so is a dotfile or a directory that is not a
  module name.
- Inside a module directory: rule files only — regular, non-dotfile, `*.md`. A dotfile, a non-Markdown file or
  a subdirectory is an error. Every `.md` file is parsed as a rule.
- Per file: the front matter must open with a `---` fence on line 1 and close with the next one; it decodes
  **strictly**, so an unknown or misspelled key is an error. `scope:` is a hard decoding error, and so is
  `notes:` (a field the schema does not define) — an unknown key is never silently dropped.
- Per file: the front-matter `id` must equal the file stem and the front-matter `module` must equal the
  directory name.
- Across files: ids are unique — the same id in two module directories is an error.
- Ordering is deterministic and independent of readdir order: module enum order (`schedule`, `game`,
  `identity`, `ledger`, `kernel`, `platform` — the declaration order of the module enum in §1), then `id`
  ascending within a module.

### Refusal mapping (house convention)

| DB surface | repository/ACL translation | HTTP |
|---|---|---|
| trigger `P0001` | `domain.ErrInvalidTransition` | 409 |
| `23514` on `rides_money_state_coupling` | `domain.ErrInvalidTransition` (mapped **by constraint name**, the other `rides` CHECKs fall through) | 409 |
| `23001` (RESTRICT delete) | `domain.ErrRecordInUse` (schedule's FK machinery) | 409 |
| `23503` FK violation | `domain.ErrNotFound` (game's birth INSERT: unknown parent) or `ErrRecordInUse` (a RESTRICT delete) | 404 / 409 |
| Go-only `ErrInvalidRoundPhase` | — | 409 |
| `ErrRoundNotOpen` (round guard) | — | 409 |
| unmapped `23514` | — | 500 (a gap: see `R-GAME-004`) |

The table is the house convention's *illustrative* set, not a closed vocabulary: any 5-character SQLSTATE may
be catalogued — the mapping is the rule, the code list is the frequency.

---

## 3. Policies (the catalog's own invariants)

The catalog's 18 machine-checked invariants are stated once, one section per policy in the checker's own
numbering, in [`policies.md`](policies.md). `go run ./cmd/spec check --strict` enforces them; this document
does not restate them.

---

## 4. Fixtures (change detectors)

A rule's guarantees are only real if each copy names the others and a change fails exactly one place first. A
**fixture** is a table of shared test vectors that every copy of a rule is run against, so a drift in any one
copy fails a test.

Today: `scoreStateRule` (`internal/game/internal/core/service/score_state_rule_test.go`) — three score
pairings (`3–1`, `1–3`, `2–2`) × both sides, asserted against the publisher's `Match.Winner`, the settlement
payload derivation, and the enforcement check. It is the model for every future fixture.

---

## 5. How to add a rule

The workflow around this section is `README.md` §1; this section is the catalog half of it — the file, and the
order the pieces appear in. There are two paths, and the first question is which one you are on: does the rule
already hold in code, or are you stating it before anything enforces it?

| path | when it is the right one | status the rule ends on |
|---|---|---|
| **A — specify first** (the default) | the rule is decided and no code enforces it yet | `specified-only` |
| **B — catalog existing behavior** | the code holds the rule today and the catalog owes it an entry | `implemented` |

Each path stands on its own: Path A needs no code to read, and Path B needs no follow-up pass. Both agree with
the `status` enum in §1 (the frozen enums) — `specified-only` is designed from the domain sources with no
code, no enforcement and no verification; `implemented` holds in code today, every named site exists, and at
least one verification pointer exists.

### Path A — specify first (the default)

The repository is spec-first: the rule file and its scenario land before the code, and the batch is shown red
for the right reason. Nothing on this path claims an enforcement site, because there is nothing yet to claim.

1. **Pick the module and the next free id** for it. Create `docs/spec/rules/<module>/<ID>.md`: the file stem
   **is** the id, the directory **is** the module (the layout is stated in §2, the rule file). Nothing else
   registers a rule — the loader walks the directory.
2. **Author the rule from the domain source.** `status: specified-only`, plus an `expert-ref` naming the
   section the rule comes from in `docs/league-tokens-learning-phases/domain-knowledge.md` or
   `docs/league-tokens-learning-phases/domain-knowledge-game-engine.md` (policy 4 gives a specified-only rule
   its expert ref and nothing else). `enforcement: []`, no `verification` pointers, and `refusal` left `null`
   unless the expert doc names one — a refusal surface nobody has designed is not a field to fill in.
   `authority` is still exactly one value (the `authority` enum in §1), because it records who *will* decide.
3. **Author its scenario**: one `@rule:<ID>` scenario under `docs/spec/features/<module>/**`, in the words
   `gherkin-style.md` allows, plus the glossary words that scenario needs (`glossary.yaml`, policy 13, step
   text stays inside the vocabulary) — the same change, not a follow-up. The scenario is the rule in
   executable form, so it is written now.
4. **Show the batch red.** Run `make audit`: the red you are looking for is the new scenario failing, because
   no code implements the rule yet. `spec check --strict` stays green on a well-formed `specified-only` rule —
   the rule is legal precisely because it claims no site (policy 4) — so the checker will not name this gap
   for you. Do not add a `//rule:<ID>` annotation to force the scenario through: an annotation in non-test Go
   code may not name a `specified-only` rule (policy 15, every Go enforcement site carries its annotation).
5. **Stop here.** The rule is catalogued, the scenario is red, and the code comes later. The follow-up pass is
   this path's second half: with the code in place, read it, fill the `enforcement` sites you verified with a
   `//rule:<ID>` annotation on each (policy 15, both directions), record the `refusal` chain and the
   `verification` pointers you grepped, and flip `status` to `implemented` (policy 3 then requires those sites
   and pointers to resolve today).

### Path B — catalog existing behavior

The rule already holds in code and the catalog owes it an entry. Fill the front matter from the code, not from
the docs: what the code does *is* the rule, and a document that disagrees is drift to fix (§1, the frozen
enums, where the domain sources win).

1. **Pick the module and the next free id** for it, and create `docs/spec/rules/<module>/<ID>.md` — the file
   stem **is** the id, the directory **is** the module (the layout is stated in §2, the rule file).
2. **Fill the front matter from the code**: `statement` in business language, one `authority`, the
   `enforcement` sites you verified today, the `refusal` chain, and `verification` pointers you grepped. Leave
   `fixture`, `knowledge-ref`, `stale_pointer` and `tie` at `null` unless one applies (the field table in §2).
3. **Set `status: implemented`**, which is legal only because the code exists — policy 3 demands at least one
   enforcement site and one verification pointer, and every pointer must resolve today. A pointer that
   resolves to nothing is not a pointer: grep the test before you write it.
4. **Add its scenario**: one `@rule:<ID>` scenario under `docs/spec/features/<module>/**`, plus the glossary
   words that scenario needs (`glossary.yaml`, policy 13, step text stays inside the vocabulary) — the same
   change, not a follow-up. It is green on the first run, because the code already refuses what the rule says;
   a scenario that fails here means the code and the rule disagree, which is `status: drifted` with the
   disagreement named in the rule's notes (policy 5).
5. **Put a `//rule:<ID>` annotation on each Go enforcement site** (policy 15, every Go enforcement site
   carries its annotation — both directions: the site names the rule, the annotation names a catalog rule).

## 6. Check it worked

Both paths end at the same gate, and all three commands below are green before the batch is done:

- `go run ./cmd/spec check --strict` (the `make spec/check` target) reports no ERROR: every policy holds,
  every pointer resolves into the tree, and the feature tree and the catalog agree in both directions.
- `make spec/gen` regenerates `docs/spec/coverage.md` from the rule files; nothing under `rules/` is ever
  machine-written and `make spec/diff` fails until the dashboard is regenerated.
- `make audit/static` is green, which is the DB-free half of the gate and includes the dashboard's freshness
  check and the strict check. It does not run the scenarios, so on Path A it stays green while the new
  scenario is still red in `make audit`'s test half — that red is the expected state of a rule stated ahead of
  its code, not a catalog failure.

A lane that names rule ids also carries a VERIFIED prove verdict before the merge: the prove registry's bypass
scenarios for each named rule ran and passed, or a git-attributed signoff covers it, and
`make prove args='--rule <ID>'` is not part of `make audit` even though it is the merge-back gate (the
workflow in `docs/spec/README.md` §1 and §7 owns that gate).
