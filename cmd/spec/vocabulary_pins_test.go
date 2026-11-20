package main

// Red-first pins for the policy 13 vocabulary hardening. One test per fix,
// each labelled with the fix it belongs to:
//
//	TestPinnedDenylistSurvivesUnderscores        — fix A (denylist over the `_`-split stream)
//	TestPinnedGlossaryRejectsDisguisedTerms      — fix B (the glossary may not launder a disguise)
//	TestPinnedBareThreeDigitQuantityOrStatus     — fix C (a three-digit figure, quantity vs status)
//	TestPinnedDocStringAndTableCellVocabulary    — fix D (docstrings and data-table cells are scanned)
//	TestPinSQLHomographAdjacency                 — fix E (SQL homographs fire on adjacency, not alone)
//	TestPinnedQuantityFollowerIsClosed           — fix F (the quantity follower set is closed)
//	TestPinSabotageObjectNamesPass                — fix G (§8 object names keep their phrase fragments)
//	TestPinDisguisedStatusNarrowed               — fix H (only HTTP-shaped lettered figures are disguises)
//	TestPinGlossaryRejectsConcatenatedTransport   — fix I (concatenated transport words are not addable)
//
// Fixtures are synthetic checkouts (t.TempDir via newTestRepo): an inline
// glossary plus one feature file, so a test fails for exactly the rule it pins.

import (
	"strings"
	"testing"
)

// pinGlossary is the synthetic checkout's allowlist. It declares every word the
// pinned features say on purpose, and nothing they say by accident: the words
// under test (`status_code`, `http_status`, `2oo`, `select`, `json`, `payload`) are absent
// so a test can prove the denylist, not the allowlist, catches them.
const pinGlossary = `common:
  - a
  - an
  - and
  - by
  - for
  - from
  - in
  - into
  - is
  - no
  - of
  - on
  - one
  - the
  - to
  - with
  - zero
game:
  - answer
  - carries
  - entries
  - insert
  - ledger
  - locks
  - matches_ended_at_check
  - names
  - owner
  - player
  - pool
  - posts
  - reads
  - refusal
  - refused
  - ride
  - rides
  - rides_state_gate
  - says
  - shape
  - status
  - tokens
  - tries
  - users
  - write
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// vocabularyPinnedRepo writes an inline glossary and one feature file into a
// synthetic checkout, then runs policy 13 over it.
func vocabularyPinnedRepo(t *testing.T, glossary, feature string) *testRepo {
	t.Helper()

	repo := newTestRepo(t)
	repo.addFile(t, "docs/spec/glossary.yaml", glossary)
	repo.addFile(t, "docs/spec/features/game/pinned.feature", feature)
	return repo
}

func joinedVocabularyMessages(t *testing.T, repo *testRepo) string {
	t.Helper()
	_, messages := vocabularyFindings(t, repo)
	return strings.Join(messages, "\n")
}

// TestPinnedDenylistSurvivesUnderscores is fix A: underscore-joined words are a
// single tokenizer token (a database object name must stay one glossary word),
// but the DENYLIST also reads the token split on `_` — otherwise `status_code`,
// `http_status`, `2OO` and a keyword hidden inside an object name walk straight
// past it and the only remedy offered is "add it to the glossary".
func TestPinnedDenylistSurvivesUnderscores(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinGlossary, `@game
Feature: The denylist cannot be glued together

  @rule:R-KERNEL-001
  Scenario: The wire is described in one token
    Then the answer carries the status_code
    And the answer carries the http_status

  @rule:R-KERNEL-002
  Scenario: A status figure written with letters
    Then the answer says 2OO

  @rule:R-KERNEL-003
  Scenario: The database object keeps its SQL fragment
    When the Owner pool tries to insert a ride
    Then the write is refused by matches_ended_at_check

  @rule:R-KERNEL-004
  Scenario: A facade step hides the keyword in a name
    Then the answer names matches_ended_at_check
`)

	joined := joinedVocabularyMessages(t, repo)

	for _, want := range []string{
		`step uses "status code"`,
		`step uses "http", which describes the wire`,
		`step token "2oo" is a numeric status`,
		`step uses the SQL word "check"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not mention %q:\n%s", want, joined)
		}
	}
	// The disguise must not fall through to the "add it to the glossary"
	// remedy: a disguised term is transport vocabulary, not a missing word.
	for _, unwanted := range []string{
		`step token "status_code" is not declared`,
		`step token "http_status" is not declared`,
		`step token "2oo" is not declared`,
		`step token "matches_ended_at_check" is not declared`,
	} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("findings offer the glossary remedy for a disguise (%q):\n%s", unwanted, joined)
		}
	}
	// The sabotage exemption still covers the SQL fragment of a database
	// object name: `matches_ended_at_check` in a §8 step is a name, not a
	// query.
	if strings.Contains(joined, "pinned.feature::The database object keeps its SQL fragment") {
		t.Errorf("a sabotage step's database object name was flagged:\n%s", joined)
	}
}

// TestPinnedGlossaryRejectsDisguisedTerms is fix B: the remedy "add it to the
// glossary" must not permanently whitelist the disguise. A term that spells a
// denylisted phrase / transport word over its `_`-split form, or that is a
// status figure written with letters, is a policy 13 ERROR naming the section
// and the term, and the term never enters the allowlist.
func TestPinnedGlossaryRejectsDisguisedTerms(t *testing.T) {
	repo := vocabularyPinnedRepo(t, `common:
  - the
game:
  - insert
  - matches_ended_at_check
  - ride
  - status_code
  - http_status
  - 2oo
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`, `@game
Feature: A glossed disguise

  @rule:R-KERNEL-001
  Scenario: The ride is locked
    Then the ride is locked
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	joined := describeFindings(c.findings)

	for _, want := range []string{
		`glossary term "status_code" in section "game"`,
		`glossary term "http_status" in section "game"`,
		`glossary term "2oo" in section "game"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not mention %q:\n%s", want, joined)
		}
	}
	for _, term := range []string{"status_code", "http_status", "2oo"} {
		if g.sections["game"][term] {
			t.Errorf("glossary term %q entered the allowlist despite being a disguise", term)
		}
	}
	// The database object names and the sabotage family's SQL words are the
	// glossary's legitimate cargo (the pinned V1 exemption): they stay.
	for _, term := range []string{"matches_ended_at_check", "insert"} {
		if !g.sections["game"][term] {
			t.Errorf("glossary term %q was rejected; database object names and §8 words are legitimate", term)
		}
	}
}

// TestPinnedBareThreeDigitQuantityOrStatus is fix C: a three-digit figure in
// the HTTP range is a QUANTITY when the next token is one of the curated
// quantity followers — units (`tokens`) and the closed business-noun set
// (`entries`) — and a numeric STATUS at the end of a step or before a transport
// noun ("refused as 409", "409 status"). The follower set's closure (fix F) is
// pinned by TestPinnedQuantityFollowerIsClosed; this pin keeps the two curated
// cases and the step-end/transport-noun cases working.
func TestPinnedBareThreeDigitQuantityOrStatus(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinGlossary, `@game
Feature: Quantities and statuses are different figures

  @rule:R-KERNEL-001
  Scenario: Two figures quantify what follows
    Given the player locks 100 tokens on "Team A"
    And the ledger posts 200 entries

  @rule:R-KERNEL-002
  Scenario: A figure before a transport noun is a status
    Then the answer carries 409 status
    And the answer says 409
`)

	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")

	if strings.Contains(joined, `bare number "100"`) {
		t.Errorf("100 tokens was flagged as a status:\n%s", joined)
	}
	if strings.Contains(joined, `bare number "200"`) {
		t.Errorf("200 entries was flagged as a status, but it quantifies a business noun:\n%s", joined)
	}
	group := findingsFor(findings, policyVocabulary)
	if len(group) != 1 {
		t.Fatalf("%d findings, want 1 (the two 409s dedupe under one scenario):\n%s",
			len(group), joined)
	}
	if !strings.Contains(joined, `bare number "409"`) {
		t.Errorf("the transport-noun and step-end figures were not flagged:\n%s", joined)
	}
	if !strings.Contains(group[0].RuleID, "pinned.feature::A figure before a transport noun is a status") {
		t.Errorf("the finding names %q, want the 409 scenario", group[0].RuleID)
	}
}

// TestPinnedDocStringAndTableCellVocabulary is fix D: policy 13 read only step
// text, so a docstring carrying `SELECT * FROM users` or a data-table cell
// carrying `status_code` passed. Both are step arguments of the same step and
// are attributed to the same `file::scenario`; the quoted-literal exemption does
// NOT apply inside a docstring body, and the sabotage exemption covers a
// sabotage step's own argument.
func TestPinnedDocStringAndTableCellVocabulary(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinGlossary, `@game
Feature: Prose and cells are vocabulary too

  @rule:R-KERNEL-001
  Scenario: A docstring smuggles a query
    Then the answer reads
      """
      SELECT * FROM users
      """

  @rule:R-KERNEL-002
  Scenario: A docstring's quotes do not launder the wire
    Then the answer reads
      """
      the payload is "json"
      """

  @rule:R-KERNEL-003
  Scenario: A cell smuggles a phrase
    Then the answer reads:
      | the shape   |
      | status_code |

  @rule:R-KERNEL-004
  Scenario: A sabotage step still writes raw SQL in its docstring
    When the Owner pool tries to insert a ride
    Then the write is refused by rides_state_gate
      """
      insert into rides
      """
`)

	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")

	if !strings.Contains(joined, `step uses the SQL word "select"`) {
		t.Errorf("a docstring carrying SQL vocabulary was not scanned:\n%s", joined)
	}
	for _, want := range []string{`step uses "json"`, `step uses "payload"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("a quoted docstring body was exempted (%q missing):\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, `step uses "status code"`) {
		t.Errorf("a data-table cell carrying a phrase was not scanned:\n%s", joined)
	}
	if strings.Contains(joined, "pinned.feature::A sabotage step still writes raw SQL in its docstring") {
		t.Errorf("a sabotage step's docstring was flagged for SQL vocabulary:\n%s", joined)
	}
	// Attribution: the docstring/cell findings name the scenario exactly as a
	// verification.gherkin pointer does.
	for _, f := range findings {
		if !strings.HasPrefix(f.RuleID, "docs/spec/features/game/pinned.feature::") {
			t.Errorf("finding %q names %q, want a file::scenario pointer", f.Message, f.RuleID)
		}
	}
}

// pinHomographGlossary declares every word the adjacency pins say on purpose,
// including the homographs themselves (`check`, `table`, `unique`, `index`,
// `create`, `constraint`): a homograph is ordinary English, so the glossary may
// carry it. What policy 13 must decide is whether the STEP used it as SQL.
const pinHomographGlossary = `common:
  - a
  - an
  - and
  - by
  - each
  - for
  - from
  - in
  - into
  - is
  - of
  - on
  - one
  - the
  - to
  - with
game:
  - answer
  - check
  - column
  - constraint
  - create
  - day
  - game
  - index
  - insert
  - key
  - match
  - matches_ended_at_check
  - names
  - owner
  - pair
  - passes
  - pool
  - refused
  - ride
  - rides
  - set
  - table
  - teams
  - tries
  - unique
  - write
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinSQLHomographAdjacency is fix E: the SQL half of the denylist is
// partitioned. A HOMOGRAPH — a word that is both SQL vocabulary and ordinary
// English (`check`, `unique`, `index`, `references`, `create`, `table`,
// `column`) — errors only on adjacency: a neighbour in the same step's token
// stream is itself SQL vocabulary or one of the database object nouns (`key`,
// `database`, `schema`, `row`). A HARD keyword (`select`, `insert`, `update`,
// `delete`, `join`, `drop`, `alter`, `constraint`, `trigger`, `grant`,
// `revoke`, `cascade`, `upsert`, `truncate`) errors alone, as it always did.
// The §8 sabotage exemption is unchanged and still covers every SQL word.
func TestPinSQLHomographAdjacency(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinHomographGlossary, `@game
Feature: SQL vocabulary is a shape, not a spelling

  @rule:R-KERNEL-001
  Scenario: A check in plain English
    Then the check passes

  @rule:R-KERNEL-002
  Scenario: A business fact about uniqueness
    Then each pair of teams is unique

  @rule:R-KERNEL-003
  Scenario: A table set for match day
    Then the table is set for match day 1

  @rule:R-KERNEL-004
  Scenario: A hard keyword alone
    Then insert into game.rides

  @rule:R-KERNEL-005
  Scenario: Two SQL words create a table
    Then create table

  @rule:R-KERNEL-006
  Scenario: A check beside its constraint
    Then check constraint

  @rule:R-KERNEL-007
  Scenario: A unique index
    Then unique index

  @rule:R-KERNEL-008
  Scenario: A unique key
    Then the unique key is set

  @rule:R-KERNEL-009
  Scenario: A database name hides a keyword
    Then the answer names matches_ended_at_check

  @rule:R-KERNEL-010
  Scenario: The sabotage family keeps its SQL words
    When the Owner pool tries to insert a ride
    Then the write is refused by the table check
`)

	findings, _ := vocabularyFindings(t, repo)
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.RuleID] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a)-(c) A homograph alone is English, not SQL: no finding at all.
	for _, scenario := range []string{
		"A check in plain English",
		"A business fact about uniqueness",
		"A table set for match day",
	} {
		t.Run("not SQL: "+scenario, func(t *testing.T) {
			if got := byScenario[pointer(scenario)]; got != "" {
				t.Errorf("a homograph used as English was flagged:\n%s", got)
			}
		})
	}

	// (d) A hard keyword still errors alone — the guard that keeps the
	// partition from becoming a general amnesty.
	t.Run("SQL: a hard keyword alone", func(t *testing.T) {
		if got := byScenario[pointer("A hard keyword alone")]; !strings.Contains(got, `step uses the SQL word "insert"`) {
			t.Errorf("a hard keyword was not flagged:\n%s", got)
		}
	})

	// (e) Adjacency fires: SQL word beside SQL word, and a homograph beside a
	// database object noun.
	for _, pin := range []struct {
		scenario string
		want     []string
	}{
		{"Two SQL words create a table", []string{`SQL word "create"`, `SQL word "table"`}},
		{"A check beside its constraint", []string{`SQL word "check"`, `SQL word "constraint"`}},
		{"A unique index", []string{`SQL word "unique"`, `SQL word "index"`}},
		{"A unique key", []string{`SQL word "unique"`}},
	} {
		t.Run("SQL: "+pin.scenario, func(t *testing.T) {
			got := byScenario[pointer(pin.scenario)]
			for _, want := range pin.want {
				if !strings.Contains(got, want) {
					t.Errorf("findings do not mention %q:\n%s", want, got)
				}
			}
		})
	}

	// The fragment fix A pinned: an underscore-joined database name stays
	// SQL-shaped, so the keyword it hides still fires.
	t.Run("SQL: a homograph inside a database name", func(t *testing.T) {
		got := byScenario[pointer("A database name hides a keyword")]
		if !strings.Contains(got, `SQL word "check"`) {
			t.Errorf("the keyword inside a database name was not flagged:\n%s", got)
		}
	})

	// (f) The §8 exemption is unchanged: no SQL word in a sabotage step, even
	// the adjacent pair.
	t.Run("exempt: the sabotage family", func(t *testing.T) {
		got := byScenario[pointer("The sabotage family keeps its SQL words")]
		for _, unwanted := range []string{"insert", "table", "check"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("a sabotage step was flagged for SQL vocabulary (%q):\n%s", unwanted, got)
			}
		}
	})
}

// TestPinnedQuantityFollowerIsClosed is fix F: the quantity exemption is a
// CLOSED set, not "any business word". A three-digit HTTP-range figure followed
// by a token outside quantityUnits ∪ {entries, rows} is a numeric status again
// (`409 conflict`, `200 ok`, `404 not`), while the curated followers keep
// quantifying (`100 tokens`, `200 entries`, `200 rows`). An earlier fix
// generalised the follower to any a–z word and reopened the §6 status ban it
// sits inside; this pin holds the closure shut from both sides.
func TestPinnedQuantityFollowerIsClosed(t *testing.T) {
	repo := vocabularyPinnedRepo(t, `common:
  - the
game:
  - answer
  - conflict
  - entries
  - holds
  - ledger
  - locks
  - not
  - ok
  - on
  - player
  - posts
  - rows
  - says
  - tokens
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`, `@game
Feature: Only the curated followers quantify a figure

  @rule:R-KERNEL-001
  Scenario: A figure before a dispute
    Then the answer says 409 conflict

  @rule:R-KERNEL-002
  Scenario: A figure before an acknowledgement
    Then the answer says 200 ok

  @rule:R-KERNEL-003
  Scenario: A figure before a negation
    Then the answer says 404 not

  @rule:R-KERNEL-004
  Scenario: The curated followers quantify
    Given the player locks 100 tokens on "Team A"
    And the ledger posts 200 entries
    And the ledger holds 200 rows

  @rule:R-KERNEL-005
  Scenario: A bare figure
    Then the answer says 409
`)

	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.RuleID] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a)-(c) A follower outside the closed set is not a quantity: the figure is
	// the numeric status §6 forbids. The finding must be the denylist's, not the
	// allowlist's — every step word here is declared.
	for _, pin := range []struct {
		scenario string
		figure   string
		follower string
	}{
		{"A figure before a dispute", "409", "conflict"},
		{"A figure before an acknowledgement", "200", "ok"},
		{"A figure before a negation", "404", "not"},
	} {
		t.Run("status: "+pin.scenario, func(t *testing.T) {
			got := byScenario[pointer(pin.scenario)]
			if !strings.Contains(got, `bare number "`+pin.figure+`"`) {
				t.Errorf("%s %s was not flagged as a numeric status:\n%s", pin.figure, pin.follower, got)
			}
			if strings.Contains(got, `"`+pin.follower+`" is not declared`) {
				t.Errorf("the follower was caught by the allowlist, not the status rule:\n%s", got)
			}
		})
	}

	// (d) The curated followers still quantify, and a bare figure is still the
	// status it always was.
	t.Run("quantity: the curated followers", func(t *testing.T) {
		if got := byScenario[pointer("The curated followers quantify")]; got != "" {
			t.Errorf("a curated quantity follower was flagged:\n%s", got)
		}
	})
	t.Run("status: a bare figure", func(t *testing.T) {
		got := byScenario[pointer("A bare figure")]
		if !strings.Contains(got, `bare number "409"`) {
			t.Errorf("a figure with nothing after it was not flagged:\n%s", got)
		}
	})

	// The unit case never leaks: `100 tokens` is the only 100 in the fixture.
	if strings.Contains(joined, `bare number "100"`) {
		t.Errorf("a curated quantity unit was flagged as a status:\n%s", joined)
	}
}

// pinSabotageGlossary declares the words the §8 object-name pin says on
// purpose, including the two database object names it names — the glossary is
// where gherkin-style.md §8 puts them, so a finding here is the denylist's.
const pinSabotageGlossary = `common:
  - a
  - an
  - and
  - by
  - for
  - from
  - in
  - into
  - is
  - of
  - on
  - one
  - the
  - to
  - with
game:
  - answer
  - carries
  - code
  - present
  - refused
  - rides_http_gate
  - rides_status_code_present_check
  - status
  - write
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinSabotageObjectNamesPass is fix G: gherkin-style.md §8 promises that a
// sabotage assertion names a real database object (`rides_status_code_present_check`),
// and rules.schema.md policy 13 says those names surface only in §8 steps, which
// the sabotage exemption covers. The single-token half of the exemption already
// holds; the PHRASE pass had no sabotage guard, so the `status code` inside such
// a name errored. Within a sabotage step the phrase must not fire on fragments
// glued by `_`; the plain words in a facade step, and the transport half of a
// name, still error exactly as before.
func TestPinSabotageObjectNamesPass(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinSabotageGlossary, `@game
Feature: A database object name keeps its fragments

  @rule:R-KERNEL-001
  Scenario: A check constraint name glues the wire words
    Then the write is refused by rides_status_code_present_check

  @rule:R-KERNEL-002
  Scenario: A facade step says the words plainly
    Then the answer carries the status code

  @rule:R-KERNEL-003
  Scenario: The transport half still applies to a name
    Then the write is refused by rides_http_gate
`)

	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.RuleID] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a) The §8 object name is a name, not a query and not a phrase: no
	// finding at all, and in particular no "add it to the glossary" remedy for
	// the name the glossary already declares.
	t.Run("sabotage: an object name passes", func(t *testing.T) {
		got := byScenario[pointer("A check constraint name glues the wire words")]
		if got != "" {
			t.Errorf("a sabotage step's database object name was flagged:\n%s", got)
		}
	})

	// (b) The exemption is scoped to the glued fragments of a sabotage step:
	// the same words written as words in a facade step are the phrase §6 bans.
	t.Run("facade: the plain words still error", func(t *testing.T) {
		got := byScenario[pointer("A facade step says the words plainly")]
		if !strings.Contains(got, `step uses "status code"`) {
			t.Errorf("a facade step's plain phrase was not flagged:\n%s", got)
		}
	})

	// (c) The transport half is absolute, sabotage or not: a name that carries
	// `http` still speaks the wire.
	t.Run("sabotage: the transport half still applies", func(t *testing.T) {
		got := byScenario[pointer("The transport half still applies to a name")]
		if !strings.Contains(got, `step uses "http", which describes the wire`) {
			t.Errorf("a transport word inside a sabotage object name was not flagged:\n%s", got)
		}
	})

	// The phrase exemption must not turn into an allowlist hole: the declared
	// object name never falls through to the glossary remedy.
	if strings.Contains(joined, `step token "rides_status_code_present_check" is not declared`) {
		t.Errorf("the declared object name fell through to the glossary remedy:\n%s", joined)
	}
}

// pinDisguiseGlossary declares the words the disguised-status pin says on
// purpose, including the digit-leading tokens an earlier pattern flagged as
// disguised HTTP figures (`1st`, `30min`, `3d`) and the one that really is
// disguised (`2oo`).
const pinDisguiseGlossary = `common:
  - a
  - after
  - an
  - and
  - by
  - for
  - from
  - in
  - is
  - of
  - on
  - one
  - the
  - to
  - with
game:
  - "1st"
  - "30min"
  - "2oo"
  - "3d"
  - answer
  - finishes
  - player
  - says
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinDisguisedStatusNarrowed is fix H: `disguisedStatusPattern` used to be
// "any token that begins with a digit and mixes letters in", so `1st`, `30min`
// and `3d` were numeric statuses and could never be declared — in the glossary
// or in a step. §6's ban is about the three-digit HTTP figure respelled with
// letters (`2oo` for `200`, `4O4` for `404`), so the pattern is narrowed to
// exactly three characters, a leading 1–5, then digits or the letter `o`. The
// status-like figures stay refused on BOTH paths (glossary term and step
// token); the ordinary digit-leading words are unflagged and addable.
func TestPinDisguisedStatusNarrowed(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinDisguiseGlossary, `@game
Feature: A digit-leading word is not a status

  @rule:R-KERNEL-001
  Scenario: An ordinal and a duration are business facts
    Given the player finishes 1st after 30min

  @rule:R-KERNEL-002
  Scenario: A lettered status is still transport
    Then the answer says 4O4
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	glossaryFindings := describeFindings(c.findings)
	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.RuleID] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a) The digit-leading words are not disguises: the glossary accepts them
	// and a step may say them.
	for _, term := range []string{"1st", "30min", "3d"} {
		if strings.Contains(glossaryFindings, `glossary term "`+term+`"`) {
			t.Errorf("glossary term %q was refused as a disguised status:\n%s", term, glossaryFindings)
		}
		if !g.sections["game"][term] {
			t.Errorf("glossary term %q did not enter the allowlist:\n%s", term, glossaryFindings)
		}
	}
	t.Run("step: an ordinal and a duration pass", func(t *testing.T) {
		got := byScenario[pointer("An ordinal and a duration are business facts")]
		if got != "" {
			t.Errorf("`1st` / `30min` were flagged in a step:\n%s", got)
		}
	})

	// (b) The guard: the real HTTP lookalikes are still refused, on both paths
	// and in any case.
	if !strings.Contains(glossaryFindings, `glossary term "2oo" in section "game" is not a legitimate step word: it is a numeric status`) {
		t.Errorf("the glossary accepted the letterspelled status `2oo`:\n%s", glossaryFindings)
	}
	if g.sections["game"]["2oo"] {
		t.Error("glossary term \"2oo\" entered the allowlist despite being a disguise")
	}
	for _, token := range []string{"4o4"} {
		if !strings.Contains(byScenario[pointer("A lettered status is still transport")], `step token "`+token+`" is a numeric status`) {
			t.Errorf("step token %q was not a numeric status:\n%s", token, joined)
		}
	}
	if !disguisedStatusPattern.MatchString("4O4") || !disguisedStatusPattern.MatchString("2OO") {
		t.Error("the pattern is no longer case-insensitive: 4O4 and 2OO must be disguised statuses")
	}
}

// TestPinGlossaryRejectsConcatenatedTransport is fix I: splitting the denylist
// stream on `_` closed the `status_code` hatch, but a bare concatenation carries
// no `_` at all, so `httpstatus` and `statuscode` stayed whole and the glossary
// remedy ("add it to the glossary") laundered them permanently. disguiseReason
// now tries every two-part cut and refuses a term whose BOTH halves are
// denylisted transport vocabulary. The halves are transport only — SQL words are
// deliberately excluded — so a database object name such as
// `rides_tokens_locked_check` stays addable.
func TestPinGlossaryRejectsConcatenatedTransport(t *testing.T) {
	repo := vocabularyPinnedRepo(t, `common:
  - the
game:
  - httpstatus
  - rides_tokens_locked_check
  - ride
  - statuscode
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`, `@game
Feature: A glossed concatenation

  @rule:R-KERNEL-001
  Scenario: The ride is locked
    Then the ride is locked
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	joined := describeFindings(c.findings)

	for _, pin := range []struct {
		term   string
		reason string
	}{
		{"httpstatus", `concatenates the denylisted transport words "http" + "status"`},
		{"statuscode", `concatenates the denylisted transport words "status" + "code"`},
	} {
		t.Run("rejected: "+pin.term, func(t *testing.T) {
			if !strings.Contains(joined, `glossary term "`+pin.term+`" in section "game"`) {
				t.Errorf("glossary term %q was accepted:\n%s", pin.term, joined)
			}
			if !strings.Contains(joined, pin.reason) {
				t.Errorf("the reason for %q does not name the halves (%q missing):\n%s",
					pin.term, pin.reason, joined)
			}
			if g.sections["game"][pin.term] {
				t.Errorf("glossary term %q entered the allowlist despite being a concatenated disguise", pin.term)
			}
		})
	}

	// The guard: the object names §8 asserts and the glossary declares are not
	// concatenations of transport words, and the check must not reach them.
	t.Run("addable: a database object name", func(t *testing.T) {
		if !g.sections["game"]["rides_tokens_locked_check"] {
			t.Errorf("glossary term \"rides_tokens_locked_check\" was rejected:\n%s", joined)
		}
	})
}
