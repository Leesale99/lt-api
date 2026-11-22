# Business-rule catalog — schema

`docs/spec/rules/` is the **canonical, hand-edited** catalog of the project's business
rules: one authored Markdown file per rule at `<module>/<ID>.md` — a strict YAML front
matter between two `---` fences holding the machine fields this document specifies,
followed by an optional authored body whose `## Notes` section is the rule's notes.
`docs/spec/business-rules.md` is **generated** from those files: a thin dashboard of
totals and links into the rule files. Never edit it.

The catalog exists so that every rule answers three questions in one place:

1. **What kind of rule is it** and therefore **who is the single authority** (the
   decision owner) for it.
2. **Where is it enforced today** — the sites, and whether each site is the authority
   or a copy that must be tied back to it.
3. **How do we know it still holds** — the tests that exist *today*, and the expert
   document the rule came from.

Vocabulary follows the vault's lessons: *Four kinds of rule decide where logic lives*,
*Source of truth per invariant*, *A rule with two enforcement points has zero*, and
*Tactical design heuristics*.

> **Naming note.** `repository` (the vault Lessons call this layer *store*; the Go type
> was renamed `Store` → `Repository` in the modular-monolith refactor).

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

The one reading test: *what breaks if nobody checks this rule?* Unrepairable wrong data
reachable by any writer → database. A caller acting before it is allowed, or a formula
that changes with the product → domain (checked **at the service door** for any caller).
Ordering/locking/atomicity mechanics → **repository**. Request framing → handler.

### `authority` — the single decision owner (exactly one per rule)

| value | meaning |
|---|---|
| `database` | A constraint or a trigger refuses the write. |
| `domain` | The domain operation / pure function decides; callers may not bypass it. |
| `service` | The use case decides the order and the transaction boundary. |
| `repository` | The repository executes the atomic write and its write-instant gates. |
| `composition-root` | Cross-context wiring: which facade/ACL each context receives and the direction of every dependency. |
| `handler` | The transport frames and refuses the request. |

`category` names the **default**; `authority` records the **actual** decision owner.
A rule whose authority deviates from its category's default MUST say why in its
`## Notes` (policy 8). Deviations are the interesting entries — they are where a rule
migrated across a boundary (e.g. `R-GAME-010` left the database because its trigger read
another context's table).

#### Database vs repository authority

Both refuse a forbidden write, but by different means, and the difference decides what
proves the rule.

| | `database` | `repository` |
|---|---|---|
| **path dependence** | **Path-independent**: the CHECK / trigger / unique / composite-FK fires on every write from any path — the application, `psql`, a future service, a backfill script. | **Path-dependent**: the predicate, CAS, `FOR SHARE` or guard-function call lives inside specific repository code paths. A *new* query that forgets the guard bypasses the rule silently. |
| **verification** | A sabotage scenario that attempts the forbidden write through the Owner pool, bypassing application code entirely, and asserts the refusal. | Call-set pinning fixtures that prove every writing query in the repository includes the guard, so a new ungated path fails a test. |

Choose `database` whenever the predicate can be stated row-locally without policy: it
is the only authority that survives a writer you have not written yet. Choose
`repository` when the predicate needs Go (cross-context facts, an ordering decision, a
computed value) — and pay for it with a call-set fixture, because the guards are only
as good as the set of queries that remember to call them.

### `mechanism` — only when `authority: database`

| value | meaning |
|---|---|
| `constraint` | `CHECK` / `NOT NULL` / `UNIQUE` / `FK` — row-local, declarative, preferred. |
| `trigger` | Only what a constraint cannot say: cross-row reads, `OLD` vs `NEW`, `INSERT`-only predicates, in-tx sequencing. |
| `composite-fk` | Cross-table consistency without procedural code. |

Sub-ordering inside the database: **declarative constraints first**, triggers only for
what a constraint cannot express. Policy never moves into SQL — a pricing formula in
PL/pgSQL is a rule nobody can see, test or rename.

Seeded reference data takes no mechanism value of its own: rows inserted by an applied
migration (e.g. `R-IDENTITY-014`'s roles, permission codes and grants) are catalogued as
`mechanism: constraint`, because what refuses a conflicting seed is the keys and what
keeps the seeded values true is migration immutability. (Gate B5 — no `seed` /
`reference-data` value is added.)

### `write_scope` — decides constraint vs trigger

| value | meaning |
|---|---|
| `row-local` | The predicate references only `NEW` — a `CHECK` can express it. |
| `cross-row` | Needs `OLD`, a sibling row, or a group aggregate. |
| `cross-table` | Needs another table's facts. In a modular monolith that is usually a boundary smell — see `boundary`. |

The front-matter key is `write_scope:` (it was `scope:` in the retired single-file
catalog; the old key is now a hard error — see §2). The **values** are unchanged, and
the field is still about *what the write reads*, never about which module or run the
rule belongs to — that is what `module` and `expert-ref` are for.

### `enforcement[].layer` — where the site lives

| value | meaning |
|---|---|
| `handler` | Transport: DTO validation, status mapping, headers. |
| `composition-root` | Cross-context wiring / facade binding — the composition-root layer where modules are handed their collaborators (a `cmd/...` wiring function, a facade binding, an ACL adapter chosen at assembly). It also names the startup/assembly seam, where a program is composed once at boot (the `main.go` that calls the wiring function, resolves configuration and starts the process) — so a fact about what happens exactly once before anything is served belongs here. Distinguished from `handler`, which is transport framing: nothing here sees a request. (Gate B5.) |
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

**Copy policy.** Exactly one `authority` layer. A non-authoritative site is allowed only
if it names what ties it to the authority:

- `pinned_by: <fixture>` — a shared change-detector fixture (see §4), **or**
- `pinned_by: null` **and** the rule's `tie:` field (§2 — it replaces the retired
  `Tie:` sentence convention in `notes`) showing the copy cannot drift silently:
  **defense-in-depth** must
  fail loudly at the authority (wrong copy ⇒ the constraint/trigger refuses and its own
  test fails first); **advisory-copy** is exempt by definition — it only shapes the
  refusal, the authority still refuses the write.

If neither holds for a data-carrying copy, the entry is a second copy of the rule —
which the vault counts as zero. The remedy is `status: drifted` with the missing tie
named in the rule's `## Notes`.

### `status`

| value | meaning |
|---|---|
| `implemented` | The rule holds in code today, every named site exists, and at least one verification pointer exists. |
| `specified-only` | Designed in a phase document; no code yet. `expert-ref` required, no `enforcement`, no `verification`. |
| `drifted` | The rule's **content** disagrees between the expert docs and actual behavior: the code refuses what the doc allows (or vice versa), the formula/window/ordering differs, or a data-carrying copy has no tie. The `## Notes` body says what disagrees and which side the catalog believes. Notes required. |

**Rot is not drift.** A *rotted pointer* — a change-detector naming a dead file, a
comment pointing at a deleted test, enforcement that moved away from where the docs
claim it lives — does NOT make a rule `drifted`: its content still holds. Keep `status:
implemented` and record the rot in the rule's `stale_pointer` field (§2) — policy 5
refuses that field on any other status. Both `drifted` rules and every `stale_pointer`
MUST be surfaced by `spec/check` (and `spec queue`).

Applied migrations are immutable history; when rot is caused by stale prose in an
applied migration, the fix is the catalog (and a comment at the current site), not a
rewrite of the migration.

### `module`

`schedule` | `game` | `identity` | `ledger` | `kernel` | `platform`

### Mapping from the coarser enums

The coarse enum values `authority: db|app|both` and
`layer: validator|domain|service|db|middleware` map onto this schema's taxonomy: the
five-value authority and six-value layer name *which* app layer. The table below records
the mapping. It is a reading aid, not a transform: a rule file carries this schema's
values directly (the retired single-file catalog's coarse values are gone), and the
threadbare dashboard prints no per-rule fields at all.

| coarse | this schema |
|---|---|
| `authority: db` | `database` |
| `authority: app` | `domain` \| `service` \| `repository` \| `composition-root` \| `handler` (the actual value) |
| `authority: both` | not a valid state: one `authority` + N `enforcement` entries |
| `layer: validator` | `validator` (own value as of A2; use `handler` for inline DTO checks) |
| `layer: db` | `db-constraint` or `db-trigger` |
| `layer: middleware` | `handler` |

---

## 2. The rule file

A rule is one file: `docs/spec/rules/<module>/<ID>.md`. The directory names the module
and the file stem names the id, so the path and the entry can never disagree. Here is a
real one (`schedule/R-SCHEDULE-025.md`), abridged — shortened prose, fewer pointers:

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

The front matter is the machine state: exactly the fields below, decoded strictly. It
is a block of YAML between two `---` fences at the very top of the file and holds
exactly **one** YAML document.

| field | type | required | notes |
|---|---|---|---|
| `id` | string | yes | `R-<MODULE>-NNN`, flat per module, stable once assigned. Must equal the file's stem. Never renumber. A retired rule's file is deleted from the catalog and recorded in `rules/README.md`'s retired-ids note; its id is never reused (gate B5 — supersedes "retired rules are marked, not deleted"). |
| `title` | string | yes | Short imperative name ("Rides are born locked"). |
| `statement` | string (block) | yes | 1–3 sentences of business language. What the business demands, no code-speak. It is a **front-matter** field, not body prose (ADR-030: the statement stays machine-readable, so the generator and `spec brief` can print it). |
| `module` | enum | yes | Owner context of the *rule*, not of every enforcement site. Must equal the name of the directory the file sits in (§2 loader strictness). |
| `entity` | string | yes | Table or aggregate the rule is about (`ride`, `balance`, `journal_entry`). |
| `category` | enum | yes | §1. |
| `status` | enum | yes | §1. |
| `authority` | enum | yes | Exactly one. |
| `mechanism` | enum \| null | iff `authority: database` | §1. |
| `write_scope` | enum \| null | required for `authority: database`; optional elsewhere | §1. Renamed from `scope:` — the old key is a hard error. Outside the database it records the predicate's *shape*, which is often the reason the rule left the DB (`R-GAME-010`: `cross-table` is exactly why 000017 moved it to Go). |
| `enforcement` | list | yes (may be empty only for `specified-only`) | Each item: `site`, `layer`, `role`, `pinned_by`. |
| `enforcement[].site` | string | yes | Go: `path::Symbol` (`internal/game/internal/core/domain/ride.go::Ride.Create`). SQL: the migration id (`migrations/000012_add_rides_insert_state_gate.up.sql`). **Never line numbers.** |
| `enforcement[].layer` | enum | yes | §1. |
| `enforcement[].role` | enum | yes | §1. |
| `enforcement[].pinned_by` | string \| null | yes (explicit) | Fixture id. `null` is allowed only with the copy policy above: a `tie:` note for defense-in-depth, nothing needed for advisory-copy. |
| `refusal` | map \| null | recommended | See the refusal table below. Omitted for rules that cannot refuse (pure computation) and for `specified-only` rules whose refusal surface is undesigned. |
| `refusal.code` | string | in refusal | A 5-character SQLSTATE, or the reserved word `synthetic` (a Go-only refusal with no SQLSTATE). **Illustrative, not a closed list:** `P0001`, `23514`, `23503`, `23001` are the codes the house table below shows most often; any real SQLSTATE (`23505`, `23502`, `22003`, `42501`, …) is valid. `cmd/spec` warns — never errors — on a code outside the illustrative set (gate B5). |
| `refusal.constraint` | string \| list \| null | in refusal | The named refusal (constraint name, or the name the trigger `RAISE`s). A list when a rule carries several constraints. |
| `refusal.sentinel` | string \| null | in refusal | The domain sentinel the repository/ACL maps to (`ErrInvalidTransition`, …). `null` = unmapped. |
| `refusal.http` | int \| null | in refusal | Status the handler writes; must be a real status (100–599). `null` = falls through to 500. |
| `fixture` | string \| null | no | The shared change-detector fixture id this rule's copies are pinned to. Every `pinned_by` in the catalog must name this id or another rule's `fixture`. |
| `verification.unit` | list of strings | yes for `implemented` (one of three) | `path::TestSymbol` (add `::Subtest` text only if the subtest name is stable). One of `unit`/`e2e`/`gherkin`: **any one** satisfies policy 3. Must exist **today** — grep it. |
| `verification.e2e` | list of strings | no | `path::TestSymbol` for existing E2E coverage. Counts toward policy 3 exactly like `unit`; a rule proved only by an e2e test is covered. Must exist **today**. |
| `verification.gherkin` | list of strings | no | `<feature-file>::<scenario name>` under `docs/spec/features/**`; every scenario carries an `@rule:R-...` tag. Counts toward policy 3 like `unit`/`e2e`; policy 10 resolves every pointer against the feature tree. |
| `expert-ref` | string (path) | required for `specified-only` | The phase document the rule comes from. Omitted where no doc covers the rule (an ADR referenced only in code is not a path that exists in this repo). |
| `knowledge-ref` | string (path) | no | The knowledge-base note behind the rule, relative to the repository root. It must exist and sit under `docs/league-tokens-learning-phases/` (policy 16). It is **additional** to `expert-ref`, never a replacement: a `specified-only` rule keeps its `expert-ref` and may add this beside it. |
| `stale_pointer` | string (block) \| null | no | A **rot** note: pointers that no longer resolve (a dead file, a deleted test, enforcement that moved). Legal **only** on `status: implemented` — policy 5 refuses it on any other status (`stale-pointer note on a <status> rule`). It replaces the retired `stale-pointer:` prefix that used to live inside `notes`; the two are not interchangeable and the old prefix in prose is just prose. |
| `tie` | string (block) \| null | no | How a data-carrying copy is tied to its authority: the sentence that shows it cannot drift silently (wrong copy ⇒ the authority refuses, and its own test fails first). Policy 7 accepts a non-null `tie` in place of a `pinned_by` fixture for a `defense-in-depth` site; `advisory-copy` needs neither. It replaces the retired `Tie:` sentence convention inside `notes`. |

There is **no `notes` field.** The rule's notes are the prose under the body's `## Notes`
heading; `notes:` in the front matter is a hard decoding error (see below).

### The body

Everything after the closing `---` fence is authored prose — Markdown, at the author's
discretion. What the loader reads out of it is exactly one thing: the prose under the
single `## Notes` heading, which becomes the rule's notes. That is the section policies
5 and 8 look at (`drifted` ⇒ notes; `authority` ≠ category default ⇒ notes), and the
one `spec brief` prints. Policy 7's tie is **not** read from here: it is the `tie`
front-matter field.

- A body may carry further `##` sections; they are documentation, not machine state,
  and the loader ignores them. Notes end at the next level-2 heading.
- A **non-empty body with no `## Notes` heading is an error** — that prose would
  otherwise be invisible to the loader. A rule with nothing to say has no body at all
  (the last fence line ends the file).
- `notes`, `stale_pointer` and `tie` are compared in canonical form: every whitespace
  run collapses to one space and leading/trailing whitespace is dropped, so an author's
  line wrapping can never change the decoded value.

### The loader's strictness

The directory layout is strict, because a misplaced file would silently drop a rule:

- Top level: one directory per module, named by a value of the frozen module enum.
  The single exception is `README.md` at the rules-directory top level — authored prose
  about the layout, skipped by the loader and not a rule. Any other non-directory entry
  is an error, and so is a dotfile or a directory that is not a module name.
- Inside a module directory: rule files only — regular, non-dotfile, `*.md`. A dotfile,
  a non-Markdown file or a subdirectory is an error. Every `.md` file is parsed as a
  rule.
- Per file: the front matter must open with a `---` fence on line 1 and close with the
  next one; it decodes **strictly**, so an unknown or misspelled key is an error.
  `scope:` (the retired name) and `notes:` (a field that no longer exists) are therefore
  hard errors, not silently dropped keys.
- Per file: the front-matter `id` must equal the file stem and the front-matter `module`
  must equal the directory name.
- Across files: ids are unique — the same id in two module directories is an error.
- Ordering is deterministic and independent of readdir order: module enum order
  (`schedule`, `game`, `identity`, `ledger`, `kernel`, `platform` — the declaration order
  of §1), then `id` ascending within a module.

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

The table is the house convention's *illustrative* set, not a closed vocabulary: any
5-character SQLSTATE may be catalogued (gate B5) — the mapping is the rule, the code
list is the frequency.

---

## 3. Policies (the catalog's own invariants)

`cmd/spec check` verifies all of these mechanically — the policy ids below match
`cmd/spec/check.go`.

1. `id` matches `^R-[A-Z]+-\d{3}$` and is unique; per-module numbering is flat.
2. Every rule has exactly one `category`, one `status`, one `authority`.
3. `status: implemented` ⇒ at least one `enforcement` site **and** at least one
   `verification` pointer, and every `verification` path/symbol exists today.
4. `status: specified-only` ⇒ `expert-ref` present, `enforcement: []`, no
   `verification`, and no `refusal` unless the expert doc names one.
5. `status: drifted` ⇒ the rule's notes (the `## Notes` body) are present and name the
   disagreement. `stale_pointer` (the rot note) is legal **only** when `status:
   implemented`: on any other status `cmd/spec` reports `stale-pointer note on a
   <status> rule` (checker id `5-drifted`).
6. `authority: database` ⇒ `mechanism` non-null and `write_scope` non-null (the message
   is `database authority requires a write_scope`). Any other authority ⇒ `mechanism`
   null (`write_scope` may still be set, see §2).
7. Every `enforcement` entry with `role` ≠ `authoritative` carries `pinned_by`
   (non-null) **or** a non-null `tie` note; the message is
   `non-authoritative site <site> has no pinned_by and no tie note`. `advisory-copy` is
   exempt from both (§1 copy policy). A data-carrying copy with neither is a second copy
   of the rule — the vault counts that as zero — so the remedy is `status: drifted` with
   the missing tie named in notes.
8. `authority` ≠ category default ⇒ the rule's notes are present (`authority <a>
   deviates from category <c> default <d> without notes`).
9. `site`/`verification` use `path::Symbol`, never line numbers, never a bare package.
10. Every `verification.gherkin` pointer is `<feature-file>::<scenario name>` and
    resolves to a real scenario under `docs/spec/features/` (checker id `10-gherkin`).
11. `pinned_by` names a fixture declared by some rule's `fixture` field.
12. The feature tree is cross-checked in both directions — every `@rule` tag names a
    catalog rule, and every scenario names exactly one (checker id `12-features`).
13. Every step in `docs/spec/features/**` is bounded by the vocabulary, in both
    directions (checker id `13-vocabulary`):
    - **Denylist (absolute for transport).** A step may not use the transport
      words of `gherkin-style.md` §6 — `http`, `https`, `json`, `request`,
      `response`, `header`, `headers`, `endpoint`, `route`, `handler`, `dto`,
      `payload`, `status code` — or a bare three-digit HTTP-range figure (`200`,
      `401`, `409`, `422`, `500`). A three-digit figure is a QUANTITY rather than a
      status only when the very next token of the step is one of the closed set of
      units and business nouns `tokens`, `rounds`, `matches`, `minutes`, `hours`,
      `days`, `seconds`, `players`, `teams`, `members`, `points`, `entries`, `rows`
      (`100 tokens`, `200 entries`, `200 rows`). The set is curated and closed: the
      figure either quantifies one of those nouns or it is the transport fact §6
      forbids — a figure at the end of a step (`refused as 409`) and a figure
      before any other token (`409 status`, `409 conflict`, `200 ok`) are numeric
      statuses.
    - **SQL vocabulary (adjacency).** The §6 SQL words are partitioned, because some
      of them are ordinary English:
      - **Homographs** — `check`, `unique`, `index`, `references`, `create`,
        `table`, `column` — never error **alone**: `the check passes`, `each pair
        of teams is unique` and `the table is set for match day 1` are business
        prose. A homograph fires only on **adjacency**: it is an ERROR iff the
        neighbouring token of the same step is itself SQL vocabulary — homograph
        or hard keyword — or one of the database object nouns `key`, `database`,
        `schema`, `row`. Adjacency is i±1 **after skipping the closed stopword
        set** `a`, `an`, `the`, `new`, `temporary`, `each`, `every`, `per`: the
        walk passes over consecutive stopwords, the first non-stopword
        neighbour decides, and the boundary of the token stream is not
        adjacency. So `create table`, `create a table`, `create a new table`,
        `check constraint`, `unique index` and `unique key` are ERRORs.
      - **Hard keywords** — `select`, `insert`, `update`, `delete`, `join`,
        `drop`, `alter`, `constraint`, `trigger`, `grant`, `revoke`, `cascade`,
        `upsert`, `truncate` — have no English reading in this domain and error
        alone, exactly as before: `insert into game.rides` is an ERROR.
      Matching is on **exact tokens** and does no stemming, which is what gives the
      two halves the asymmetry above: a hard keyword errors **alone**, a homograph
      only on adjacency. Their inflected forms (`deletes`, `updates`, `inserting`,
      `created`) are therefore ordinary words that trigger nothing on their own —
      they bind through the glossary below like every other undeclared word.
      - The SQL half is **scoped to facade steps**: the sabotage family of
        `gherkin-style.md` §8 (`the Owner pool …`, `the write is refused by …`,
        `the refusal names …`, `the write is allowed`) exists to bypass the
        application with raw writes and legitimately says `insert`; no SQL word,
        homograph or hard, is reported there, and the exemption is decided once
        per step and covers its step arguments. The transport half applies to it.
      The denylist also reads each token **split on `_`**, so joining words is not a
      way past it: `status_code` denies as the phrase `status code`, `http_status` as
      the transport word `http`, and a figure respelled with letters (`2OO`) as the
      numeric status it imitates. A database object name's fragments are scanned that
      way too, and a homograph fragment is SQL by the **form** of the identifier it
      belongs to — `check` in `matches_ended_at_check` is an ERROR without needing a
      neighbour, since splitting names must not reopen the `_` bypass — and such
      names surface only in §8 steps, where the sabotage exemption covers them **only
      when the name is REAL**: a `refusal.constraint` or `refusal.sentinel` value in a
      rule file under `docs/spec/rules/`, or a name that appears literally in a
      `migrations/*.sql`
      file. A fabricated name gets the phrase denylist normally, in a sabotage step as
      anywhere else.
    - **Allowlist (the seeded glossary).** Every remaining token of every step must be
      declared in `docs/spec/glossary.yaml` — in the section named after the feature
      file's module directory (`game`, `identity`, `schedule`, `ledger`, `kernel`,
      `platform`, one section each even where the module has no feature file yet) or
      in the shared `common` section. An undeclared token is an ERROR naming
      `file::scenario` and the section to extend. The seed describes the vocabulary
      the tree uses today; extending it is part of the change that needs the word.
      A term that merely respells denylisted **transport** vocabulary over its `_`-split
      form (`status_code`, `http_status`), that glues any two or more such words
      together with no separator at all (`httpstatus`, `httpstatuscode`,
      `statuscodehttp`), or that is a status figure written with letters in place of
      digits — the three-character figure, a leading 1–5 and then digits or the letter
      `o` (`2oo`, `4O4`, `2o0`), or that same figure with its trailing `o`s standing in
      for nothing (`200o`, `2o0o`) — is an ERROR naming the section and the term, and never
      enters the allowlist: the remedy may not launder the disguise. The SQL half of the
      denylist is deliberately **not** applied to glossary terms, so the database object names
      the sabotage steps declare (`rides_tokens_locked_check`) stay addable here.
    - **Tokenizer.** Lowercase; the English possessive suffix (`'s`) folds away;
      quoted literals (`"Spring 2026"`, `"2–1"`, `"ana@example.com"`) are data and are
      dropped; every remaining run of characters other than a letter, a digit or an
      underscore becomes a separator. Underscore is *not* a separator, so a database
      object name (`rides_state_gate`) is one token and is seeded like any other word.
      A step's **docstring body and data-table cells** are part of the step: the same
      rules scan them and the same `file::scenario` pointer reports them. A docstring
      body keeps its quotation marks (there they are prose, not data); a table cell
      keeps the quoted-literal exemption.
14. `category` binds the verification and refusal surface of an **implemented** rule
    (checker id `14-category`); `specified-only` rules are exempt:
    - `interface` ⇒ a `refusal` is present and `refusal.http` is non-null (an
      interface rule names the status the framed-away request is refused with),
      and `verification.gherkin` is forbidden (its proof is a Go test).
    - `refusal-mapping` ⇒ `verification.gherkin` is forbidden.
    - `persistence-fact` ⇒ `verification.gherkin` is forbidden **and** at least one
      `verification.unit` pointer is required.
    - `invariant` / `policy` ⇒ a WARN when there is no `verification.gherkin`
      pointer: a business-visible rule wants an executable scenario, but a warning
      never fails `--strict`.
    - `orchestration` / `boundary` ⇒ unconstrained.
15. Every Go enforcement site carries its rule's annotation, and every annotation
    names a catalog rule — in both directions (checker id `15-annotations`):
    - **The annotation (convention).** A comment line that is exactly
      `//rule:R-XXX-NNN` — the prefix, the id, and nothing else (no space after the
      colon, no trailing prose; one id per line). It sits either on the declaration
      line or in the contiguous comment block directly above the declaration. A blank
      line or a non-comment line breaks contiguity, so a comment block separated
      from the declaration documents something else. A site named by several rules
      carries one `//rule:` line per id in that same block. Inside a parenthesized
      `var (...)`, `const (...)` or `type (...)` block, a spec placed directly on one of
      the grouped declarations reads its own contiguous comment first — that annotation
      wins over the block-level doc and never covers a sibling — and a spec with none of
      its own falls back to the block-level doc. `gofmt` reflows a doc comment's
      `//rule:` to `// rule:`, which is the same annotation; the id after
      the colon must still be exact, so `// rule: R-XXX-NNN` stays a typo.
    - **Catalog → tree.** For every implemented rule's Go enforcement site
      (`path::Symbol`), the rule's id must appear in that annotation window; an
      annotation that is not at the declaration does not count. SQL sites (paths
      starting `migrations/`) are exempt — the migration's own text is the record —
      and test files are exempt too, because a test names its rule through
      `verification`. A missing annotation is an ERROR.
    - **Tree → catalog.** Every annotation in any non-vendor `*.go` file under the
      repository must resolve to a catalog rule id (test files are included in this
      direction; `testdata/` is excluded as fixture data). An annotation whose id
      does not match `^R-[A-Z]+-\d{3}$` is an ERROR of its own, so a typo cannot pass
      for "a rule that does not exist yet"; the report labels it `(malformed id)`
      and labels a well-formed id that names no catalog rule `(unknown id
      R-X-NNN)`, so neither reads as a real rule id. An annotation in **non-test**
      Go code that resolves to a `specified-only` rule is an ERROR too: the rule has
      no code yet, so no site may claim it. Test files keep the reverse-direction
      check only — their annotation must resolve, but a specified-only target is
      legal there because a test names its rule through `verification`.
16. `knowledge-ref`, when present, must (a) exist as a file relative to the
    repository root and (b) be a path under `docs/league-tokens-learning-phases/`
    (checker id `16-knowledge-ref`). It records the knowledge-base note behind the
    rule; `expert-ref` is unchanged by it, so `specified-only` rules keep their
    `expert-ref` and may add `knowledge-ref` beside it.

18. Scenarios must assert something an observer can see, and the assertion must
    match the rule's category (checker id `18-oracle`). It is a cross-cutting
    collector over the parsed feature tree, like policy 13:
    - **No vacuous Then.** A Then step — and every `And`/`But` continuation of one,
      which gherkin types as an outcome step — may not only report that the code
      did not explode: `runs without throwing`, `does not panic`,
      `completes successfully`, `the call succeeds`, `no error occurs` and their
      close variants are ERRORs. Such a step would still pass after the rule was
      deleted. Findings name the scenario's `file::scenario` pointer and the
      offending step.
    - **Category binding.** Policy 14's binding is reused: an `interface` rule with
      at least one executable scenario must have at least one scenario whose Then
      asserts a refusal observable (the refused-write family, refusal vocabulary,
      or one of the rule's `refusal.constraint` / `refusal.sentinel` names). An
      `invariant` or `policy` rule with at least one executable scenario must have
      at least one scenario with a non-vacuous Then outcome (a state or side
      effect), because those categories are business-visible. `orchestration`,
      `boundary`, `refusal-mapping` and `persistence-fact` bind nothing here and
      are exempt; a rule with no executable scenario is outside policy 18 (policy
      14 already reports its missing proof, and `specified-only` rules have no
      scenario by construction).
    - **No re-derived expectations.** A Then step's quoted string, decimal number,
      or integer above the scenario-coordinate range (1–38) that appears verbatim
      in the executable Go source at one of the rule's annotated enforcement sites
      is an ERROR: the expected value was copied from the implementation and can
      no longer disagree with it. "Executable" is precise: literals are collected
      from function bodies only, never from `const`/`var`/`type` declarations
      (domain vocabulary such as `"locked"` legitimately lives there) or from
      comments. A bare integer 1–38 is treated as a scenario coordinate (match
      day / round number), not an asserted value. SQL migration sites and test
      files contribute no literals, and a rule with no Go enforcement site is
      skipped silently. The detector is deliberately a static, same-literal test:
      it catches a scenario that spells the implementation's constant, not a
      scenario whose expected value is merely computed the same way.

---

## 4. Fixtures (change detectors)

A rule's guarantees are only real if each copy names the others and a change fails
exactly one place first. A **fixture** is a table of shared test vectors that every copy
of a rule is run against, so a drift in any one copy fails a test.

Today: `scoreStateRule` (`internal/game/internal/core/service/score_state_rule_test.go`) —
three score pairings (`3–1`, `1–3`, `2–2`) × both sides, asserted against the publisher's
`Match.Winner`, the settlement payload derivation, and the enforcement check. It is the
model for every future fixture.

---

## 5. How to add a rule

The end-to-end recipe (gate, glossary, annotations) is `README.md` §2 — the workflow
entry point. This section is the catalog half of it: the file, and the order the pieces
appear in.

1. **Pick the module and the next free id** for it. Create
   `docs/spec/rules/<module>/<ID>.md`: the file stem **is** the id, the directory **is**
   the module (see §2). Nothing else in the tree needs to be touched to register a rule
   — the loader walks the directory.
2. **Fill the front matter from the code, not from the docs**: `statement` in business
   language, one `authority`, the `enforcement` sites you verified today, the `refusal`
   chain, and `verification` pointers you grepped. Leave `fixture`, `knowledge-ref`,
   `stale_pointer` and `tie` at `null` unless one applies (§2).
3. **Add its scenario**: one `@rule:<ID>` scenario under
   `docs/spec/features/<module>/**`, plus the glossary words that scenario needs
   (`glossary.yaml`, policy 13) — the same change, not a follow-up.
4. **Run `make audit` while the rule is still unimplemented.** It must be red *for the
   right reason*: the scenario fails and `spec check --strict` names the missing site or
   test pointer (an untraceable entry fails the gate too).
5. **Implement the rule**, and put a `//rule:<ID>` annotation on each Go enforcement site
   (policy 15, both directions).
6. **Run `make spec/gen`.** The dashboard `docs/spec/business-rules.md` is regenerated;
   the rule file is not — nothing under `rules/` is ever machine-written.
7. **Run `make audit` again.** Green, including `spec/diff`, is done.

## 6. Open decisions (user gate A2)

> Resolved at gate A2 (D1–D6) and closed at gate B5; the resolutions are recorded in
> the catalog's own rule notes.
