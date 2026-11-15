# Gherkin style guide — the executable specification

`docs/spec/rules.yaml` says **what** the business rules are and where they are enforced. A
`.feature` file says what those rules look like **from outside**, in words a product reader
can check without opening Go: one capability, one file, scenarios the harness runs against
real Postgres. The catalog is canonical for rule *content*; this file is canonical for *how
a scenario is written*. If they disagree about wording, fix the scenario; if about the rule,
fix the catalog.

---

## 1. Files and features

| item | convention |
|---|---|
| path | `docs/spec/features/<module>/<capability>.feature` — `<module>` is a catalog module |
| `<capability>` | lowercase with underscores — a coherent slice of behaviour (`ride_lifecycle`) |
| `Feature:` | exactly one per file: the feature **is** the capability, tagged `@<module>` |

A capability is a verb-run over one entity: the life of a ride from lock to terminal, the
settlement of a round, the calendar's freeze. If two candidate rules would never be read
together by the same reader, they belong in two files. The `Feature:` description under the
header states the business intent in the expert's words — prose, not steps, and the place
where the phase documents' vocabulary earns its keep.

---

## 2. The rule tag — one scenario, one rule

Every scenario carries **exactly one** `@rule:R-<MODULE>-<NNN>` tag, and every tag resolves
to a real catalog entry:

```sh
grep -F -- "- id: R-GAME-002" docs/spec/rules.yaml   # must print the entry
```

- **Preferred shape: 1:1** — one scenario per rule, tag on the scenario.
- **A rule that needs several scenarios** (legal vs illegal, facade vs database, one row per
  boundary value) groups them under `Rule:` — the `@rule` tag sits on the `Rule:` line and
  its scenarios inherit it. Scenarios under a tagged `Rule:` carry no `@rule` tag of their
  own, and no scenario carries a second rule's tag.
- A rule is proved in exactly one feature file; another file may mention it in prose but may
  not tag it. A scenario that proves no rule does not belong in these files.

---

## 3. Scenario names

- **Unique within the file.** The pair `path::name` is how the catalog's
  `verification.gherkin` pointer addresses a scenario:
  `docs/spec/features/game/ride_lifecycle.feature::A new ride is born locked`.
- A human sentence, sentence case, no rule id, no "should", no code words: it reads as the
  business claim — *A new ride is born locked*, not *TestCreateRide*. Reusing the rule's
  `title` verbatim is allowed but not required; the name must be falsifiable on its own.

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

## 6. Vocabulary — the phase documents' words, nothing from the wire

A reader must be able to verify intent without knowing Go, SQL or the transport. Use the
domain's own vocabulary: **ride, lock, unlock, burn, continue, arrive, settle, wait on a
decision, round, match, season, team, player, match day, tokens, stake, bonus, streak, TB,
committed result, open round, refused**.

Forbidden in feature and step text — they describe *how* a message is shaped, not what the
business decides: `HTTP`, `JSON`, `request`, `response`, `status code`, `endpoint`, `route`,
`handler`, `DTO`, `payload`, numeric statuses (`200`, `401`, `409`, `422`, `500`), and SQL
vocabulary in facade steps (`SELECT`, `INSERT`, `UPDATE`, `CHECK`, `WHERE`, table and column
names).

Say **refused** instead of "409"; **the player locks tokens** instead of "the POST /v1/rides
route"; **the ride is locked** instead of "the body carries state=locked". Database object
names are allowed **only** in sabotage assertions (§8) — the one place where the database is
the subject of the sentence. Concrete nouns, not abstractions: `"Spring 2026"`, `"Team A"`,
`"Ana"`, `match day 1`. Numbers: tokens whole (`10 tokens`), money to two decimals (`9.60`,
`1.80`), scores as `2–1`.

---

## 7. Step family A — facade steps

Drives the module's own entry points over real Postgres: real migrations, real roles, nothing
stubbed, no hand-written SQL. It is the default family; a feature with no facade scenario is
not proving business behaviour.

| step | shape |
|---|---|
| `Given` | `Given a\|the <world noun> <fact or state>` — builds the world, may act |
| `When` | `When the <actor> <domain verb> <object> [<qualifier>]` — one action |
| `Then` | `Then the <subject> <is\|reads\|carries\|keeps\|has> <observable>` — one fact |
| `Then` | `Then the <command> is refused [as <named refusal>]` — the only refusal shape |

actors: `player`, `calendar`, `settlement` (`Owner pool` only in §8). Domain verbs, as the
facades expose them: *lock tokens, continue the ride, burn, unlock, arrive, settle, record
the result, open/close/complete a round, register a player, pair teams, end a match*.

- One action per `When`; no "do X and Y" steps. `And` continues the same family and tense.
- Assert what a product owner can see — the ride's state, its stake, its bonus, its streak,
  whether a refusal happened and **which** refusal it is — never a row count, a column, an
  error string. A `Then … is refused` names the business refusal ("as an illegal
  transition", "as the round not being open", "as the wrong phase"); the status is not it.
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
| `Then` | `Then the refusal names <raised name>` — only when the gate's own name differs |
| `Then` | `Then the write is allowed` — the carve-out case |

- One forbidden write per scenario. `<database object>` is spelled exactly as the migration
  spells it: a trigger gate (`rides_state_insert_gate`, `rides_state_gate`) or a declarative
  constraint (`rides_money_state_coupling`, `rides_tokens_locked_check`).
- Sabotage is only for rules whose catalog `authority` is `database` (or where the database
  holds a load-bearing copy). For a `domain`- or `service`-authority rule a bypassing writer
  is **allowed by design**, so a sabotage scenario would be a false promise: prove those at
  the facade and say why in a `#` comment above the `Rule:`.
- Build the row through the facade first, then sabotage *that* row; a sabotage scenario is
  still a business scenario. Never assert a Go sentinel, an error string or a status here.
- A carve-out deserves its own scenario: the state-preserving rewrite the trigger
  deliberately allows is what stops the gate from freezing legitimate writes.

```gherkin
    When the Owner pool tries to move the ride straight to "burned"
    Then the write is refused by rides_state_gate
    And the refusal names ride_state_transition_illegal
```

---

## 9. `@known-defect:<item>` — the red scenario that is a finding

A scenario that is **red because of a catalogued defect** in
`docs/spec/misalignment-report.md` §6 carries, in addition to its `@rule` tag,
`@known-defect:<item>` — the §6 item number, e.g. `@known-defect:2` for the unmapped
SQLSTATEs that surface as an unnamed server error.

```gherkin
    # Defect 2 (misalignment-report §6): a check violation from the rides table is not
    # translated into a named ride refusal, so the caller learns only that something
    # failed, never that the stake was the problem.
    @known-defect:2
    Scenario: The refused stake is not yet named for the caller
      Given it is 3 hours before round 1's first match
      When the player locks 0 tokens on "Team A" for match day 1
      Then the refusal is unnamed for the caller
      And the ride was not created
```

Semantics the harness implements:

- **default run:** skipped with a loud note —
  `KNOWN-DEFECT 2 (misalignment-report §6.2): <scenario> — skipped` — so a green run never
  silently claims the behaviour is correct;
- **opt-in run:** executed only under a flag (`SPEC_KNOWN_DEFECTS=1`);
- reported as a **finding**, not a pass and not a failure: it never breaks the build. A
  finding that starts passing means the defect is fixed — remove the tag *and* the item;
- it never counts towards a rule's coverage: the rule still needs a non-defect scenario;
- the item number must exist in §6, and the scenario body says in business words what is
  wrong today and what it should be. A catalogued defect is the only justification for a
  known-red scenario.

---

## 10. What does not belong in a feature

Mechanical field rules (format, length, rounding); concurrency interleavings needing two
sessions and a forced pause — the *observable* half belongs here ("the same result arriving
twice pays the bonus once"), the race itself in the Go race tests; wire framing (permissions,
pagination, headers); anything the facades cannot do — a rule provable only by reaching into
a package's internals is not facade-expressible, and that gets recorded in the catalog
instead of invented as a step.

---

## 11. Step inventory the pilot features standardized

```text
Given  the season "<name>" has <n> rounds
Given  the teams "<a>", "<b>" and "<c>" play in "<name>"
Given  the player "<name>" is registered in "<name>" with <n> tokens
Given  round <n> holds match day <n>: "<team>" against "<team>" [at odds <x>]
Given  it is <n> hours|minutes before|after round <n>'s first|last match
Given  a ride locked by "<player>" on "<team>" for match day <n> with <n> tokens [at odds <x>]
Given  a ride waiting on a decision for "<team>" on match day <n> with <n> tokens
Given  a ride for "<team>" on match day <n> that was burned
Given  the ride on match day <n> is in the state "<state>"
Given  match day <n> ends "<h>–<a>" [to "<team>"]          — also a When
Given  round <n> closed without settling its rides

When   the player locks <n> tokens on "<team>" for match day <n>
When   the player burns | unlocks | continues the ride
When   round <n> completes / the season's final round completes
When   the settlement reports match day <n> as a win for "<team>"
When   the result of match day <n> arrives a second time

Then   the ride is locked | waits on a decision | is lost | is burned | is unlocked
Then   the ride is locked on match day <n>
Then   the ride carries a stake of <n> tokens / carries no bonus / carries a base price
Then   the ride has accrued a bonus of <x> / keeps its bonus of <x> / has banked <n> win
Then   the ride still reads "<state>"
Then   the command | the settlement is refused as <illegal transition | the wrong phase |
       the round not being open>
Then   no ride exists for "<player>" on match day <n> / the ride was not created
Then   the refusal is unnamed for the caller                        (@known-defect only)

When   the Owner pool tries to insert a ride for "<player>" on match day <n> in state "<state>"
When   the Owner pool tries to insert a locked ride with <an amount outside the domain>
When   the Owner pool tries to move the ride straight to "<state>"
When   the Owner pool tries to move the ride onto match day <n>
When   the Owner pool tries to unlock the ride without releasing its bonus
When   the Owner pool rewrites the frozen ride without changing its state or its match
Then   the write is refused by <database object> / the refusal names <raised name>
Then   the write is allowed
```

---

## 12. Review checklist (the readability gate)

- [ ] Could a reader who knows no Go, no SQL and no HTTP state what breaks if the rule is
      removed? If not, the scenario is code-speak.
- [ ] Does every `Then` name a business fact (state, money, refusal), not a mechanism?
- [ ] Exactly one `@rule` per scenario, every id resolving in `rules.yaml`, names unique in
      the file and matching the catalog's `verification.gherkin` pointers.
- [ ] No forbidden word from §6 left in the text?
- [ ] Does every sabotage `Then` name a real database object, spelled as the migration
      spells it — and does the sabotaged rule really have a database authority?
- [ ] Does every `@known-defect:<n>` map to an existing §6 item, with a non-defect scenario
      carrying the same rule?
