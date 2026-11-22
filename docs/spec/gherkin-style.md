# Gherkin style guide — the executable specification

`docs/spec/rules/` says **what** the business rules are and where they are enforced — one
authored Markdown file per rule at `docs/spec/rules/<module>/R-XXX-nnn.md`, where the
directory is the module and the file stem is the rule id. A `.feature` file says what
those rules look like **from outside**, in words a product reader can check without
opening Go: one capability, one file, scenarios the harness runs against real Postgres
(`make test/spec`). The catalog is canonical for rule *content*; this file is canonical
for *how a scenario is written*. If they disagree about wording, fix the scenario; if
about the rule, fix the catalog.

The mechanical parts of this guide are enforced, not merely advised:
`go run ./cmd/spec check --strict` (`make spec/check`) is the verdict — the tag policy
(§2, policy 12), the pointer policy (§3, policy 10), the vocabulary (§6, policy 13) and the
sabotage mechanics (§8) all fail it. The prose sections around them (§1, §4, §5, §10) are
the reviewer's half. The ripgrep recipes in §9 are the cheap pre-check that points at a
line.

---

## 1. Files and features

| item | convention |
|---|---|
| path | `docs/spec/features/<module>/<capability>.feature` — `<module>` is a directory under `docs/spec/rules/` |
| `<capability>` | lowercase with underscores — a coherent slice of behaviour (`ride_lifecycle`) |
| `Feature:` | exactly one per file: the feature **is** the capability, tagged `@<module>` on its first line |

A capability is a verb-run over one entity: the life of a ride from lock to terminal, the
settlement of a round, the calendar's freeze. If two candidate rules would never be read
together by the same reader, they belong in two files. The `Feature:` description under the
header states the business intent in the expert's words — prose, not steps, and the place
where the expert documents behind the rules (`expert-ref`) earn their keep.

The tree holds capabilities for three modules — `game` (`ride_lifecycle`, `settlement`,
`players`, `idempotency`), `identity` (`account`, `access`) and `schedule` (`lifecycle`,
`fixtures`, `resolve`, `deletion`). The other three (`ledger`, `kernel`, `platform`) have
no capability: their implemented rules are proved by Go tests and their `specified-only`
rules have no proof to write yet. A module gets a feature file when a business-visible rule
needs one, not for symmetry.

---

## 2. The rule tag — one scenario, one rule

Every scenario carries **exactly one** `@rule:R-XXX-nnn` tag, and every tag resolves to a
real rule file — the id is the file's stem, the module is its directory:

```sh
rg --files docs/spec/rules | rg '/R-GAME-002\.md$'   # must print docs/spec/rules/game/R-GAME-002.md
```

- **Preferred shape: 1:1** — one scenario per rule, tag on the scenario.
- **A rule that needs several scenarios** (legal vs illegal, facade vs database, one row per
  boundary value) groups them under `Rule:` — the `@rule` tag sits on the `Rule:` line and
  its scenarios inherit it. Scenarios under a tagged `Rule:` carry no `@rule` tag of their
  own, and no scenario carries a second rule's tag.
- A rule is proved in exactly one feature file; another file may mention it in prose but may
  not tag it. A scenario that proves no rule does not belong in these files.
- The tag and the rule file are the two halves of one artifact: the rule file names the
  scenario in `verification.gherkin`, the scenario names the rule in `@rule`. Policy 10
  resolves the pointer, policy 12 resolves the tag, and both directions are checked.

---

## 3. Scenario names

- **Unique within the file.** The pair `path::name` is how the catalog's
  `verification.gherkin` pointer addresses a scenario:
  `docs/spec/features/game/ride_lifecycle.feature::A new ride is born locked`.
- A human sentence, sentence case, no rule id, no "should", no code words: it reads as the
  business claim — *A new ride is born locked*, not *TestCreateRide*. Reusing the rule's
  `title` verbatim is allowed but not required; the name must be falsifiable on its own.
- The name is the pointer, so it is part of the rule file's contract: renaming a scenario
  is an edit to the catalog too, and a name with a colon, a trailing space or a `#` in it
  is not addressable.

---

## 4. `Background:` — the world, not the setup

`Background:` builds the world every scenario of the file shares — season, rounds, teams,
players, the fixture calendar — through facade steps (§7). It never asserts and never
performs the behaviour under test. Anything that is one scenario's precondition (a locked
ride, a settled match, the clock) goes in that scenario's `Given`, where its presence is
visible. Half a dozen steps is the ceiling; if the shared world keeps growing, the file is
probably two files.

---

## 5. Data tables

A table is for a rule that is a **list of cases** (legal/illegal transition pairs, money
bounds, phase windows), not for a scenario with many inputs: header row, one case per row,
columns padded to align, every row independently meaningful, exact values never ranges ("a
stake of zero tokens", not "a bad stake"). A `Scenario Outline` when every row runs the same
steps; a table inside one scenario only when the rows are assertions about one artefact.

---

## 6. Vocabulary — the business's words, nothing from the wire

A reader must be able to verify intent without knowing Go, SQL or the transport. Use the
domain's own vocabulary: **ride, lock, unlock, burn, continue, arrive, settle, wait on a
decision, round, match, season, team, player, match day, tokens, stake, bonus, streak, TB,
committed result, open round, refused**.

Forbidden in step text and in a scenario name — they describe *how* a message is shaped,
not what the business decides: `HTTP`, `HTTPS`, `JSON`, `request`, `response`, `header`,
`headers`, `status code`, `endpoint`, `route`, `handler`, `DTO`, `payload`, numeric statuses
(`200`, `401`, `409`, `422`, `500`), and SQL vocabulary in facade steps (`SELECT`, `INSERT`,
`UPDATE`, `CHECK`, `WHERE`, table and column names).

Say **refused** instead of "409"; **the player locks tokens** instead of "the POST /v1/rides
route"; **the ride is locked** instead of "the body carries state=locked". Database object
names are allowed **only** in sabotage assertions (§8) — the one place where the database is
the subject of the sentence. Concrete nouns, not abstractions: `"Spring 2026"`, `"Team A"`,
`"Ana"`, `match day 1`. Numbers: tokens whole (`10 tokens`), money to two decimals (`9.60`,
`1.80`), scores as `2–1`.

This section is enforced mechanically by checker policy 13 (`13-vocabulary`,
`rules.schema.md` §3): the transport half of the denylist above is an ERROR in every
step, and every remaining step token must be declared in the module's section of
`docs/spec/glossary.yaml` (or in `common`). The one exception is a §8-shaped step: an
underscore-joined database object name there is exempt from the phrase denylist only
when the name is REAL — a `refusal.constraint` or `refusal.sentinel` value in a
rule file under `docs/spec/rules/`, or a name literally present in a `migrations/*.sql` file.
A fabricated name gets the denylist normally: the exemption covers a name the database has,
not the disguise of one. The SQL words are partitioned: the homographs that are ordinary
English too (`check`, `unique`, `index`, `references`, `create`, `table`, `column`) fire
only beside another SQL word or a database object noun (`create table`, `unique index`) —
seeing through the closed stopword set `a`, `an`, `the`, `new`, `temporary`, `each`,
`every`, `per`, so `create a table` is the same SQL shape — while `the check passes` is
fine. The rest error alone (`insert into game.rides`). A step's docstring body and
data-table cells are scanned by the same
rules, and the denylist also reads every token split on `_`, so `status_code`, `2OO` or a
keyword glued into a name is not a way past it. Extending the glossary is part of the
change that needs the word — but a term that merely respells denylisted vocabulary is
rejected, so the glossary is not a way past the denylist either.

---

## 7. Step family A — facade steps

Drives the module's own entry points over real Postgres: real migrations, real roles, nothing
stubbed, no hand-written SQL. It is the default family; a feature with no facade scenario is
not proving business behaviour.

| step | shape |
|---|---|
| `Given` | `Given a\|the <world noun> <fact or state>` — builds the world, may act |
| `When` | `When the <actor> <domain verb> <object> [<qualifier>]` — one action |
| `Then` | `Then the <subject> <is\|reads\|carries\|keeps\|has\|waits\|holds\|lists> <observable>` — one fact |
| `Then` | `Then the <subject> is refused [as <named refusal>]` — the refusal shape |
| `Then` | `Then the refusal names <the field or fact to change>` — the refusal's detail |
| `Then` | `Then no <thing> exists\|was recorded\|arrives` — a negative fact |

Actors are the business's roles, singular and named only where the scenario needs a
particular person: `player`, `organiser`, `member`, `visitor`, `administrator`,
`settlement` (`owner` — the schema-owning role — belongs to §8 alone; a `Then` may instead
make the calendar, the page, the answer or the ride the subject).
Domain verbs, as the facades expose them: *locks tokens, burns, unlocks, continues the
ride, arrives, settles, completes a round, opens/closes a round, declares a season open,
adds/schedules/postpones/corrects/prices/resolves a match day, deletes a round / season /
match day / team, registers a player, registers, activates, signs in, asks for a fresh
activation mail, opens the list of seasons / players, records a team, renames a player*.

- One action per `When`; no "do X and Y" steps. `And` continues the same family and tense.
- Assert what a product owner can see — the ride's state, its stake, its bonus, its streak,
  whether a refusal happened and **which** refusal it is — never a row count, a column, an
  error string. A `Then … is refused` names the business refusal ("as an illegal
  transition", "as the round not being open", "as the wrong phase", "as an unactivated
  account", "as a permission she does not hold"); the status is not it.
- Preconditions only a system path can build (a ride waiting on a decision, a closed round)
  are still facade steps: the harness builds them the way the product would, never by
  writing rows. Money assertions give the exact figure and which side of it moved.

```gherkin
    Given the season "Spring 2026" has 2 rounds
    Given it is 3 hours before round 1's first match
    When the player locks 10 tokens on "Team A" for match day 1
    Then the ride is locked
    When match day 1 ends "2–1" to "Team A"
    Then the ride waits on a decision with a bonus of 8.00
    When the player burns the ride
    Then the command is refused as the wrong phase
```

---

## 8. Step family B — sabotage steps

A **direct database write by the Owner pool**: the harness's unrestricted role that owns the
schema, bypassing every facade, use case and guard. That is the point — the scenario proves
the rule is **path-independent**, holding even for a writer that skips every application
layer, because the database refuses the write itself.

| step | shape |
|---|---|
| `When` | `When the Owner pool tries to <forbidden write, in domain words> <object>` |
| `Then` | `Then the write is refused by <database object>` |
| `Then` | `Then the write is refused for <the fact the store stamps>` — the store refuses a row that names its own id (the `GENERATED ALWAYS` case, SQLSTATE `428C9`); e.g. `the write is refused for the id the store stamps` |
| `Then` | `Then the refusal names <raised name>` — only when the gate's own name differs |
| `Then` | `Then the write is allowed` — the carve-out case |

- One forbidden write per scenario. `<database object>` is a gate or raise name as the
  migration spells it (`rides_state_insert_gate`, `rides_state_gate`), or a declarative
  constraint by its actual name (`rides_money_state_coupling`,
  `rides_tokens_locked_check`) — including the default name Postgres gives an unnamed
  `UNIQUE`/`PKEY` when that is the name the refusal surfaces (`user_tokens_pkey`,
  `permissions_code_key`, `roles_permissions_pkey`).
- The sabotage family is exempt from the SQL half of policy 13's step-vocabulary
  denylist (`rules.schema.md` §3 policy 13): these steps legitimately say `insert` and
  name database objects. The transport half still applies.
- Sabotage is only for rules whose catalog `authority` is `database` (or where the database
  holds a load-bearing copy). For a `domain`- or `service`-authority rule a bypassing writer
  is **allowed by design**, so a sabotage scenario would be a false promise: prove those at
  the facade and say why in a `#` comment above the `Rule:`.
- Build the row through the facade first, then sabotage *that* row; a sabotage scenario is
  still a business scenario. Never assert a Go sentinel, an error string or a status here.
- A carve-out deserves its own scenario: the state-preserving rewrite the trigger
  deliberately allows is what stops the gate from freezing legitimate writes. It has its own
  step — `When the Owner pool rewrites the frozen ride without changing its state or its
  match`, paired with `Then the write is allowed`.

```gherkin
    When the Owner pool tries to move the ride straight to "burned"
    Then the write is refused by rides_state_gate
    And the refusal names ride_state_transition_illegal
```

---

## 9. Recipes — resolving the cross-references by hand

`make spec/check` is the gate; these are the greps that get an author to the line that
fails. Each recipe runs against the tree as it stands: the rejection sweeps are silent on a
clean tree, and the SQL sweep prints only the §8 steps that are allowed those words.

```sh
# The rule file behind a tag (the id is the stem, the module the directory).
rg --files docs/spec/rules | rg '/R-GAME-002\.md$'

# The feature file that tags a rule.
rg -n '@rule:R-SCHEDULE-023' docs/spec/features

# The scenario a catalog pointer addresses.
rg -n 'Scenario: A new ride is born locked$' docs/spec/features

# One capability's skeleton — its rule groups, rule titles and scenarios.
rg -n '^\s*(@rule:|Rule:|Scenario:)' docs/spec/features/game/ride_lifecycle.feature

# Every tag in the tree resolves to a rule file (silence = clean).
rg -o --no-filename --replace '$1' '@rule:(R-[A-Z]+-[0-9]{3})' docs/spec/features \
  | sort -u \
  | while read -r id; do
      ls docs/spec/rules/*/"$id".md >/dev/null 2>&1 || echo "no rule file for $id"
    done

# Transport vocabulary and numeric statuses in a step (§6).
rg -ni '^\s*(Given|When|Then|And|But)\b.*\b(http|https|json|request|response|header|headers|endpoint|route|handler|dto|payload)\b' docs/spec/features
rg -n '^\s*(Given|When|Then|And|But)\b.*\b(200|401|409|422|500)\b' docs/spec/features

# SQL words in a step: every hit must be an Owner-pool step (§8), or it is a §6 error.
rg -ni '^\s*(Given|When|Then|And|But)\b.*\b(select|insert|update|delete|join|drop|alter|constraint|trigger|grant|revoke|cascade|upsert|truncate)\b' docs/spec/features

# Is a sabotage step's database object a real one? (§8: a rule file's refusal, or a migration.)
rg -n 'rides_state_insert_gate' docs/spec/rules migrations

# Which glossary section declares a word (§6 allowlist).
awk -v w=ride '/^[a-z]+:/{s=$0} $0=="  - "w{print s}' docs/spec/glossary.yaml
```

The steps themselves are defined under `internal/spec/`: a phrasing the tree has never used
has no step behind it, so check the definitions before inventing one (§11).

---

## 10. What does not belong in a feature

Mechanical field rules (format, length, rounding); concurrency interleavings needing two
sessions and a forced pause — the *observable* half belongs here ("the same result arriving
twice pays the bonus once"), the race itself in the Go race tests; wire framing (permissions,
pagination, headers); anything the facades cannot do — a rule provable only by reaching into
a package's internals is not facade-expressible, and that gets recorded in the catalog
instead of invented as a step.

---

## 11. The step inventory lives in the step definitions

The standardized step vocabulary lives where it cannot rot: the godog step definitions under
`internal/spec/` — `steps_ride_test.go`, `steps_schedule_test.go`, `steps_settlement_test.go`,
`steps_players_test.go`, `steps_identity_test.go`, plus the sabotage family in
`sabotage_test.go` and the world they share in `world_test.go` and `harness_test.go`. This
section intentionally does not duplicate them; a hand-maintained inventory here would drift
silently. Vocabulary rules remain §6.

`make test/spec` runs the suite against real Postgres (`make db/up` first); it needs
`LT_API_TEST_DSN` and never runs on a cached result. `make prove` is the slow gate above it:
it runs the registry's mapped godog scenarios against the intact tree — the code is never
altered — so those scenarios attempt the rule's bypass and must observe the refusal. The
registry in `docs/spec/prove.yaml` addresses its scenarios the same way the catalog does —
`<feature-file>::<scenario name>` (§3) — and a renamed scenario breaks that entry too.

---

## 12. Review checklist (the readability gate)

- [ ] Could a reader who knows no Go, no SQL and no HTTP state what breaks if the rule is
      removed? If not, the scenario is code-speak.
- [ ] Does every `Then` name a business fact (state, money, refusal), not a mechanism?
- [ ] Exactly one `@rule` per scenario, every id resolving to a file under
      `docs/spec/rules/<module>/`, names unique in the file and matching the catalog's
      `verification.gherkin` pointers.
- [ ] No forbidden word from §6 left in the text?
- [ ] Does every sabotage `Then` name a real database object — as the migration spells
      it, or by the default name the store gives it — and does the sabotaged rule really
      have a database authority?
