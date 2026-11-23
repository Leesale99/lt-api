# The catalog's own policies

A policy is a machine-checked invariant of the business-rule catalog. The catalog at
`docs/spec/rules/` is authored prose plus strict front matter, and a policy is the part
of that contract a program can decide: `go run ./cmd/spec check --strict` (the
`make spec/check` target) reports every policy violation as a finding, and `--strict`
makes an ERROR fatal. The catalog has 18 of them, one section each below. The numbers are
the checker's own numbering — its registry is `cmd/spec/policies.go` — so a finding, a
report row and a section of this file all name the same policy.

`cmd/spec` is the enforcer; this file is the statement of record. Each section states the
invariant, what the checker accepts and the remedy its finding points at; the checker's
implementation is the verdict, so where the two disagree the checker is right and this
file is a bug to fix.

An ERROR fails `--strict`; a WARN never does. The frozen enums a policy reasons about
(`category`, `authority`, `mechanism`, `write_scope`, `enforcement[].layer`,
`enforcement[].role`, `status`) and the rule file's field table are stated once in
`rules.schema.md` — §1 for the enums, §2 for the fields.

## Citing a policy

Always expand a policy's statement inline at first use, so a reader who lands on the
citing sentence does not have to open this file to know what was meant: *policy 7
(non-authoritative sites are tied by a fixture or a tie note)*, *policy 13 (step text
stays inside the vocabulary)*. Link the expansion to the policy's own section,
`docs/spec/policies.md#policy-<n>--<short-name>`; later uses in the same section may
shorten to *policy 13*.

---

## Policy 1 — Id format, uniqueness and flat per-module numbering

`id` matches `^R-[A-Z]+-\d{3}$` and is unique; per-module numbering is flat.

## Policy 2 — Exactly one category, status and authority

Every rule has exactly one `category`, one `status`, one `authority`.

## Policy 3 — Implemented rules point at sites and verification that resolve today

`status: implemented` ⇒ at least one `enforcement` site **and** at least one
`verification` pointer, and every `verification` path/symbol exists today.

## Policy 4 — Specified-only rules carry an expert ref and nothing else

`status: specified-only` ⇒ `expert-ref` present, `enforcement: []`, no `verification`,
and no `refusal` unless the expert doc names one.

## Policy 5 — Drifted rules carry notes; stale pointers only on implemented rules

`status: drifted` ⇒ the rule's notes (the `## Notes` body) are present and name the
disagreement. `stale_pointer` (the rot note) is legal **only** when `status:
implemented`: on any other status `cmd/spec` reports `stale-pointer note on a <status>
rule` (checker id `5-drifted`).

## Policy 6 — Database authority requires a mechanism and a write scope

`authority: database` ⇒ `mechanism` non-null and `write_scope` non-null (the message is
`database authority requires a write_scope`). Any other authority ⇒ `mechanism` null
(`write_scope` may still be set; `rules.schema.md` §2 states the field).

## Policy 7 — Non-authoritative sites are tied by a fixture or a tie note

Every `enforcement` entry with `role` ≠ `authoritative` carries `pinned_by` (non-null)
**or** a non-null `tie` note; the message is `non-authoritative site <site> has no
pinned_by and no tie note`. `advisory-copy` is exempt from both (the copy policy in
`rules.schema.md` §1). A data-carrying copy with neither is a second copy of the rule —
the vault counts that as zero — so the remedy is `status: drifted` with the missing tie
named in notes.

## Policy 8 — A deviating authority carries notes

`authority` ≠ category default ⇒ the rule's notes are present (`authority <a> deviates
from category <c> default <d> without notes`).

## Policy 9 — Sites and pointers are `path::Symbol`, never line numbers

`site`/`verification` use `path::Symbol`, never line numbers, never a bare package.

## Policy 10 — Every `verification.gherkin` pointer resolves to a real scenario

Every `verification.gherkin` pointer is `<feature-file>::<scenario name>` and resolves
to a real scenario under `docs/spec/features/` (checker id `10-gherkin`).

## Policy 11 — Every `pinned_by` names a declared fixture

`pinned_by` names a fixture declared by some rule's `fixture` field.

## Policy 12 — The feature tree and the catalog agree in both directions

The feature tree is cross-checked in both directions — every `@rule` tag names a catalog
rule, and every scenario names exactly one (checker id `12-features`).

## Policy 13 — Step text stays inside the vocabulary

Every step in `docs/spec/features/**` is bounded by the vocabulary, in both directions
(checker id `13-vocabulary`).

**Denylist (absolute for transport).** A step may not use the transport words of
`gherkin-style.md` §6 — `http`, `https`, `json`, `request`, `response`, `header`,
`headers`, `endpoint`, `route`, `handler`, `dto`, `payload`, `status code` — or a bare
three-digit HTTP-range figure (`200`, `401`, `409`, `422`, `500`).

A three-digit figure is a QUANTITY rather than a status only when the very next token of
the step is one of the closed set of units and business nouns `tokens`, `rounds`,
`matches`, `minutes`, `hours`, `days`, `seconds`, `players`, `teams`, `members`, `points`,
`entries`, `rows` (`100 tokens`, `200 entries`, `200 rows`).

The set is curated and closed: the figure either quantifies one of those nouns or it is
the transport fact the vocabulary section of `gherkin-style.md` (§6) forbids — a figure
at the end of a step (`refused as 409`) and a figure before any other token (`409
status`, `409 conflict`, `200 ok`) are numeric statuses.

**SQL vocabulary (adjacency).** The SQL words of `gherkin-style.md` §6 are partitioned,
because some of them are ordinary English:

- **Homographs** — `check`, `unique`, `index`, `references`, `create`, `table`,
  `column` — never error **alone**: `the check passes`, `each pair of teams is unique`
  and `the table is set for match day 1` are business prose. A homograph fires only on
  **adjacency**: it is an ERROR iff the neighbouring token of the same step is itself
  SQL vocabulary — homograph or hard keyword — or one of the database object nouns
  `key`, `database`, `schema`, `row`. Adjacency is i±1 **after skipping the closed
  stopword set** `a`, `an`, `the`, `new`, `temporary`, `each`, `every`, `per`: the walk
  passes over consecutive stopwords, the first non-stopword neighbour decides, and the
  boundary of the token stream is not adjacency. So `create table`, `create a table`,
  `create a new table`, `check constraint`, `unique index` and `unique key` are ERRORs.
- **Hard keywords** — `select`, `insert`, `update`, `delete`, `join`, `drop`, `alter`,
  `constraint`, `trigger`, `grant`, `revoke`, `cascade`, `upsert`, `truncate` — have no
  English reading in this domain and error alone, exactly as before: `insert into
  game.rides` is an ERROR.

Matching is on **exact tokens** and does no stemming, which is what gives the two halves
the asymmetry above: a hard keyword errors **alone**, a homograph only on adjacency.
Their inflected forms (`deletes`, `updates`, `inserting`, `created`) are therefore
ordinary words that trigger nothing on their own — they bind through the glossary below
like every other undeclared word.

The SQL half is **scoped to facade steps**: the sabotage family of `gherkin-style.md` §8
(`the Owner pool …`, `the write is refused by …`, `the refusal names …`, `the write is
allowed`) exists to bypass the application with raw writes and legitimately says
`insert`; no SQL word, homograph or hard, is reported there, and the exemption is
decided once per step and covers its step arguments. The transport half applies to it.

**Denylist (`_`-split forms).** The denylist also reads each token **split on `_`**, so
joining words is not a way past it: `status_code` denies as the phrase `status code`,
`http_status` as the transport word `http`, and a figure respelled with letters (`2OO`)
as the numeric status it imitates.

A database object name's fragments are scanned that way too, and a homograph fragment is
SQL by the **form** of the identifier it belongs to — `check` in
`matches_ended_at_check` is an ERROR without needing a neighbour, since splitting names
must not reopen the `_` bypass.

Such names surface only in sabotage steps (`gherkin-style.md` §8), where the sabotage
exemption covers them **only when the name is REAL**: a `refusal.constraint` or
`refusal.sentinel` value in a rule file under `docs/spec/rules/`, or a name that appears
literally in a `migrations/*.sql` file. A fabricated name gets the phrase denylist
normally, in a sabotage step as anywhere else.

**Allowlist (the seeded glossary).** Every remaining token of every step must be
declared in `docs/spec/glossary.yaml` — in the section named after the feature file's
module directory (`game`, `identity`, `schedule`, `ledger`, `kernel`, `platform`, one
section each even where the module has no feature file yet) or in the shared `common`
section. An undeclared token is an ERROR naming `file::scenario` and the section to
extend. The seed describes the vocabulary the tree uses today; extending it is part of
the change that needs the word.

A term that does any of the following is an ERROR naming the section and the term, and
never enters the allowlist: the remedy may not launder the disguise.

- It merely respells denylisted **transport** vocabulary over its `_`-split form
  (`status_code`, `http_status`).
- It glues any two or more such words together with no separator at all (`httpstatus`,
  `httpstatuscode`, `statuscodehttp`).
- It is a status figure written with letters in place of digits — the three-character
  figure, a leading 1–5 and then digits or the letter `o` (`2oo`, `4O4`, `2o0`), or that
  same figure with its trailing `o`s standing in for nothing (`200o`, `2o0o`).

The SQL half of the denylist is deliberately **not** applied to glossary terms, so the
database object names the sabotage steps declare (`rides_tokens_locked_check`) stay
addable here.

**Tokenizer.** Lowercase; the English possessive suffix (`'s`) folds away; quoted
literals (`"Spring 2026"`, `"2–1"`, `"ana@example.com"`) are data and are dropped;
every remaining run of characters other than a letter, a digit or an underscore becomes
a separator.

Underscore is *not* a separator, so a database object name (`rides_state_gate`) is one
token and is seeded like any other word. A step's **docstring body and data-table cells**
are part of the step: the same rules scan them and the same `file::scenario` pointer
reports them. A docstring body keeps its quotation marks (there they are prose, not data);
a table cell keeps the quoted-literal exemption.

## Policy 14 — The category binds the verification and refusal surface

`category` binds the verification and refusal surface of an **implemented** rule
(checker id `14-category`); `specified-only` rules are exempt:

- `interface` ⇒ a `refusal` is present and `refusal.http` is non-null (an interface rule
  names the status the framed-away request is refused with), and
  `verification.gherkin` is forbidden (its proof is a Go test).
- `refusal-mapping` ⇒ `verification.gherkin` is forbidden.
- `persistence-fact` ⇒ `verification.gherkin` is forbidden **and** at least one
  `verification.unit` pointer is required.
- `invariant` / `policy` ⇒ a WARN when there is no `verification.gherkin` pointer: a
  business-visible rule wants an executable scenario, but a warning never fails
  `--strict`.
- `orchestration` / `boundary` ⇒ unconstrained.

## Policy 15 — Every Go enforcement site carries its annotation

Every Go enforcement site carries its rule's annotation, and every annotation names a
catalog rule — in both directions (checker id `15-annotations`):

- **The annotation (convention).** A comment line that is exactly `//rule:R-XXX-NNN` —
  the prefix, the id, and nothing else (no space after the colon, no trailing prose; one
  id per line). It sits either on the declaration line or in the contiguous comment block
  directly above the declaration. A blank line or a non-comment line breaks contiguity,
  so a comment block separated from the declaration documents something else. A site
  named by several rules carries one `//rule:` line per id in that same block. Inside a
  parenthesized `var (...)`, `const (...)` or `type (...)` block, a spec placed directly
  on one of the grouped declarations reads its own contiguous comment first — that
  annotation wins over the block-level doc and never covers a sibling — and a spec with
  none of its own falls back to the block-level doc. `gofmt` reflows a doc comment's
  `//rule:` to `// rule:`, which is the same annotation; the id after the colon must
  still be exact, so `// rule: R-XXX-NNN` stays a typo.
- **Catalog → tree.** For every implemented rule's Go enforcement site (`path::Symbol`),
  the rule's id must appear in that annotation window; an annotation that is not at the
  declaration does not count. SQL sites (paths starting `migrations/`) are exempt — the
  migration's own text is the record — and test files are exempt too, because a test
  names its rule through `verification`. A missing annotation is an ERROR.
- **Tree → catalog.** Every annotation in any non-vendor `*.go` file under the repository
  must resolve to a catalog rule id (test files are included in this direction;
  `testdata/` is excluded as fixture data). An annotation whose id does not match
  `^R-[A-Z]+-\d{3}$` is an ERROR of its own, so a typo cannot pass for "a rule that does
  not exist yet"; the report labels it `(malformed id)` and labels a well-formed id that
  names no catalog rule `(unknown id R-X-NNN)`, so neither reads as a real rule id. An
  annotation in **non-test** Go code that resolves to a `specified-only` rule is an ERROR
  too: the rule has no code yet, so no site may claim it. Test files keep the
  reverse-direction check only — their annotation must resolve, but a specified-only
  target is legal there because a test names its rule through `verification`.

## Policy 16 — `knowledge-ref` exists under the learning phases

`knowledge-ref`, when present, must (a) exist as a file relative to the repository root
and (b) be a path under `docs/league-tokens-learning-phases/` (checker id
`16-knowledge-ref`). It records the knowledge-base note behind the rule; `expert-ref` is
unchanged by it, so `specified-only` rules keep their `expert-ref` and may add
`knowledge-ref` beside it.

## Policy 17 — The refusal code is sane

`refusal`, when present, is sane (checker id `17-refusal`): `refusal.code` is either the
reserved word `synthetic` or a 5-character SQLSTATE, and `refusal.http`, when set, is a
real status (100–599). A SQLSTATE outside the illustrative set in the refusal-mapping
table (`rules.schema.md` §2) is a WARN — the mapping is the rule, the code list is the
frequency — so it never fails `--strict`.

## Policy 18 — A scenario asserts something an observer can see

Scenarios must assert something an observer can see, and the assertion must match the
rule's category (checker id `18-oracle`). It is a cross-cutting collector over the parsed
feature tree, like policy 13 (step text stays inside the vocabulary):

- **No vacuous Then.** A Then step — and every `And`/`But` continuation of one, which
  gherkin types as an outcome step — may not only report that the code did not explode:
  `runs without throwing`, `does not panic`, `completes successfully`, `the call
  succeeds`, `no error occurs` and their close variants are ERRORs. Such a step would
  still pass after the rule was deleted. Findings name the scenario's `file::scenario`
  pointer and the offending step.
- **Category binding.** The binding of policy 14 (the category binds the verification and
  refusal surface) is reused: an `interface` rule with at least one executable scenario
  must have at least one scenario whose Then asserts a refusal observable (the
  refused-write family, refusal vocabulary, or one of the rule's `refusal.constraint` /
  `refusal.sentinel` names). An `invariant` or `policy` rule with at least one executable
  scenario must have at least one scenario with a non-vacuous Then outcome (a state or
  side effect), because those categories are business-visible. `orchestration`,
  `boundary`, `refusal-mapping` and `persistence-fact` bind nothing here and are exempt;
  a rule with no executable scenario is outside policy 18 (policy 14 already reports its
  missing proof, and `specified-only` rules have no scenario by construction).
- **No re-derived expectations.** A Then step's quoted string, decimal number, or integer
  above the scenario-coordinate range (1–38) that appears verbatim in the executable Go
  source at one of the rule's annotated enforcement sites is an ERROR: the expected value
  was copied from the implementation and can no longer disagree with it. "Executable" is
  precise: literals are collected from function bodies only, never from
  `const`/`var`/`type` declarations (domain vocabulary such as `"locked"` legitimately
  lives there) or from comments. A bare integer 1–38 is treated as a scenario coordinate
  (match day / round number), not an asserted value. SQL migration sites and test files
  contribute no literals, and a rule with no Go enforcement site is skipped silently. The
  detector is deliberately a static, same-literal test: it catches a scenario that spells
  the implementation's constant, not a scenario whose expected value is merely computed
  the same way.
