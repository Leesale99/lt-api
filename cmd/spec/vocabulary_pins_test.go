package main

// Red-first pins for the policy 13 vocabulary hardening. One test per fix,
// each labelled with the fix it belongs to:
//
//	TestPinnedDenylistSurvivesUnderscores        — fix A (denylist over the `_`-split stream)
//	TestPinnedGlossaryRejectsDisguisedTerms      — fix B (the glossary may not launder a disguise)
//	TestPinnedBareThreeDigitQuantityOrStatus     — fix C (a three-digit figure, quantity vs status)
//	TestPinnedDocStringAndTableCellVocabulary    — fix D (docstrings and data-table cells are scanned)
//	TestPinSQLHomographAdjacency                 — fix E (SQL homographs fire on adjacency, not alone)
//	TestPinSQLHomographStopwordAdjacency         — fix E, second half (a closed stopword set is transparent to adjacency)
//	TestPinnedQuantityFollowerIsClosed           — fix F (the quantity follower set is closed)
//	TestPinSabotageObjectNameRealityGate          — fix J (§8 object names keep their phrase fragments only when the name is real)
//	TestPinDisguisedStatusNarrowed               — fix H (only HTTP-shaped lettered figures are disguises)
//	TestPinGlossaryRejectsConcatenatedTransport   — fix I (concatenated transport words are not addable)
//	TestPinGlossaryRejectsExhaustiveConcatenation — fix K (ANY concatenation length is a disguise)
//	TestPinGlossaryRejectsTrailingOStatus         — fix L (a trailing-o figure is a disguised status)
//	TestPinTrailingOStatusDeniedInStep            — fix L (the same, on the step side; three-char shapes guarded)
//	TestPinDigitLeadingWordsStayAddable           — fix L guard (digit-leading words stay unflagged and addable)
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
	if !strings.Contains(group[0].Subject, "pinned.feature::A figure before a transport noun is a status") {
		t.Errorf("the finding names %q, want the 409 scenario", group[0].Subject)
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
		if !strings.HasPrefix(f.Subject, "docs/spec/features/game/pinned.feature::") {
			t.Errorf("finding %q names %q, want a file::scenario pointer", f.Message, f.Subject)
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
		byScenario[f.Subject] += f.Message + "\n"
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

// pinStopwordGlossary declares every word the stopword-adjacency pin says on
// purpose, the homographs themselves (`create`, `table`, `check`, `unique`)
// included: a homograph is ordinary English, so the glossary may carry it. What
// policy 13 has to decide is whether the STEP used it as SQL, never whether the
// word may be declared at all (a declared-but-denied word errors anyway, because
// the denylist runs before the glossary binding).
const pinStopwordGlossary = `common:
  - a
  - each
  - for
  - is
  - of
  - the
game:
  - can
  - check
  - create
  - game
  - new
  - pair
  - passes
  - ride
  - round
  - set
  - table
  - teams
  - temporary
  - unique
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinSQLHomographStopwordAdjacency pins the second half of fix E: adjacency
// is decided over the words a step actually commits to, not over the literal
// distance of 1 in the token stream. A CLOSED stopword set — `a`, `an`, `the`,
// `new`, `temporary`, `each`, `every`, `per` — is transparent when deciding
// adjacency: from a homograph, walk past consecutive stopwords in either
// direction, and the first non-stopword neighbour decides exactly as the i±1
// rule does today (SQL vocabulary of either kind, or one of the database object
// nouns `key`, `database`, `schema`, `row`, makes the homograph SQL; anything
// else, or the end of the stream, does not). Hard keywords keep firing alone.
//
// The hole this closes: with strict i±1, `create a table`, `create a new table`
// and `create a temporary table` read as English to the checker and as SQL to a
// human — one filler word is enough to launder the shape. The guards pin the
// other direction: skipping a stopword may only SHORTEN the walk, never invent a
// neighbour past a word that is neither SQL nor a stopword, and never across the
// boundary of the step.
//
// Strict adjacency with no stopword at all (`create table`, `unique key`, and a
// hard keyword alone) is already pinned by TestPinSQLHomographAdjacency and is
// not duplicated here.
func TestPinSQLHomographStopwordAdjacency(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinStopwordGlossary, `@game
Feature: A filler word does not launder the SQL shape

  @rule:R-KERNEL-001
  Scenario: A table created behind one filler word
    Then the game can create a table

  @rule:R-KERNEL-002
  Scenario: A new table created behind two filler words
    Then the game can create a new table

  @rule:R-KERNEL-003
  Scenario: A temporary table created behind two filler words
    Then the game can create a temporary table

  @rule:R-KERNEL-004
  Scenario: A ride created behind a filler word
    Then the game can create a ride

  @rule:R-KERNEL-005
  Scenario: A table set for the round
    Then the table is set for the round

  @rule:R-KERNEL-006
  Scenario: A business fact about uniqueness
    Then each pair of teams is unique

  @rule:R-KERNEL-007
  Scenario: A check in plain English
    Then the check passes
`)

	findings, _ := vocabularyFindings(t, repo)
	// The fixture is a closed world: every step word is declared, so an
	// undeclared-token finding would mean the pin itself is wrong, not the
	// rule. Fail loudly instead of mistaking it for SQL shape.
	for _, f := range findings {
		if strings.Contains(f.Message, "is not declared") {
			t.Fatalf("the pin declares every step word, so the only possible finding is an SQL one; got: %s", f.Message)
		}
	}

	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.Subject] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// RED today: the homograph pair is separated by the stopwords `a` / `new` /
	// `temporary`, so strict i±1 sees no SQL neighbour and the step passes. It
	// reads as SQL to a human; the stopword walk must make it an ERROR. Either
	// member of the pair firing is enough — the walk may report `create`, or
	// `table`, or both.
	for _, pin := range []struct {
		scenario string
		step     string
	}{
		{"A table created behind one filler word", "create a table"},
		{"A new table created behind two filler words", "create a new table"},
		{"A temporary table created behind two filler words", "create a temporary table"},
	} {
		t.Run("RED today: "+pin.step, func(t *testing.T) {
			got := byScenario[pointer(pin.scenario)]
			if !strings.Contains(got, `step uses the SQL word "create"`) &&
				!strings.Contains(got, `step uses the SQL word "table"`) {
				t.Errorf("step %q is SQL shape hidden behind a stopword and was not flagged:\n%s",
					pin.step, got)
			}
		})
	}

	// GREEN today and after the fix: a stopword walk never invents adjacency.
	// `ride` is not SQL vocabulary, `is` is not a stopword, and the stream
	// boundary is not a neighbour — so the walk stops and the step stays prose.
	for _, pin := range []struct {
		scenario string
		step     string
	}{
		{"A ride created behind a filler word", "create a ride"},
		{"A table set for the round", "the table is set for the round"},
		{"A business fact about uniqueness", "each pair of teams is unique"},
		{"A check in plain English", "the check passes"},
	} {
		t.Run("guard: "+pin.step, func(t *testing.T) {
			if got := byScenario[pointer(pin.scenario)]; got != "" {
				t.Errorf("step %q is English, not SQL, but was flagged:\n%s", pin.step, got)
			}
		})
	}
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
		byScenario[f.Subject] += f.Message + "\n"
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
  - rides_money_state_coupling
  - rides_purse_lock
  - rides_status_code_present_check
  - status
  - write
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// pinSabotageMigrations is the migration file the reality gate reads for the
// migrations half of the contract: a name is REAL when it appears literally in
// any migrations/*.sql. `rides_purse_lock` appears here and nowhere else.
const pinSabotageMigrations = `-- fixture only: a real database object name the vocabulary pin may spell.
ALTER TABLE rides ADD CONSTRAINT rides_purse_lock UNIQUE (purse);
`

// sabotagePinnedRepo is vocabularyPinnedRepo plus the reality sources the new
// rule reads: a catalog whose rule refuses with `rides_money_state_coupling`
// (refusal.constraint) and a migration file carrying `rides_purse_lock`
// literally. Both names are REAL; `rides_status_code_present_check` is not.
func sabotagePinnedRepo(t *testing.T, glossary, feature string) *testRepo {
	t.Helper()

	repo := vocabularyPinnedRepo(t, glossary, feature)
	rule := baseRule()
	rule.Refusal = &Refusal{
		Code:       "P0001",
		Constraint: StringList{"rides_money_state_coupling"},
		HTTP:       ptr(422),
	}
	repo.writeCatalog(t, Catalog{rule})
	repo.addFile(t, "migrations/000999_fixture.up.sql", pinSabotageMigrations)
	return repo
}

// TestPinSabotageObjectNameRealityGate is fix J: gherkin-style.md §8 promises
// that a sabotage assertion names a REAL database object, and rules.schema.md
// policy 13 exempts those names from the denylisted PHRASES. The exemption used
// to be decided by the step's SHAPE alone, so a fabricated name carrying the
// `status code` fragments (`rides_status_code_present_check`) passed even though
// it exists in no rule and no migration. The reality gate makes the exemption
// conditional: an underscore-joined name is exempt from the phrase only when it
// is REAL — a value of some rule's `refusal.constraint`, a `refusal.sentinel`
// value in docs/spec/rules.yaml, or a literal substring of a migrations/*.sql
// file. A fabricated name falls back to the normal phrase pass (`step uses
// "status code"`), while the SQL half of the §8 exemption (raw SQL words in
// sabotage steps) and the absolute transport half are unchanged.
func TestPinSabotageObjectNameRealityGate(t *testing.T) {
	repo := sabotagePinnedRepo(t, pinSabotageGlossary, `@game
Feature: A database object name keeps its fragments only when it is real

  @rule:R-KERNEL-001
  Scenario: A fabricated check constraint name glues the wire words
    Then the write is refused by rides_status_code_present_check

  @rule:R-KERNEL-002
  Scenario: A rule's constraint name is a real object
    Then the write is refused by rides_money_state_coupling

  @rule:R-KERNEL-003
  Scenario: A migration's object name is a real object
    Then the write is refused by rides_purse_lock

  @rule:R-KERNEL-004
  Scenario: A facade step says the words plainly
    Then the answer carries the status code

  @rule:R-KERNEL-005
  Scenario: The transport half still applies to a name
    Then the write is refused by rides_http_gate
`)

	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.Subject] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a) The §8 exemption is conditional on the name being REAL. A fabricated
	// name carries the `status code` fragments just like a real one, so the
	// phrase pass applies to the step normally: it is a disguised phrase, not an
	// object name.
	t.Run("sabotage: a fabricated object name errors", func(t *testing.T) {
		got := byScenario[pointer("A fabricated check constraint name glues the wire words")]
		if !strings.Contains(got, `step uses "status code"`) {
			t.Errorf("a fabricated name in a sabotage step escaped the phrase check:\n%s", got)
		}
	})

	// (b) A REAL name still passes untouched. The catalog rule's
	// `refusal.constraint` names `rides_money_state_coupling`, so the phrase
	// exemption (and the glossary declaration) stands: no finding at all, in
	// particular no "add it to the glossary" remedy.
	t.Run("sabotage: a rule's constraint name passes", func(t *testing.T) {
		got := byScenario[pointer("A rule's constraint name is a real object")]
		if got != "" {
			t.Errorf("a real refusal.constraint name was flagged:\n%s", got)
		}
	})

	// (c) The second reality source: a name that appears literally in a
	// migrations/*.sql file is REAL too.
	t.Run("sabotage: a migration's object name passes", func(t *testing.T) {
		got := byScenario[pointer("A migration's object name is a real object")]
		if got != "" {
			t.Errorf("a name living in migrations/*.sql was flagged:\n%s", got)
		}
	})

	// (d) The exemption is scoped to the glued fragments of a sabotage step:
	// the same words written as words in a facade step are the phrase §6 bans.
	t.Run("facade: the plain words still error", func(t *testing.T) {
		got := byScenario[pointer("A facade step says the words plainly")]
		if !strings.Contains(got, `step uses "status code"`) {
			t.Errorf("a facade step's plain phrase was not flagged:\n%s", got)
		}
	})

	// (e) The transport half is absolute, sabotage or not: a name that carries
	// `http` still speaks the wire.
	t.Run("sabotage: the transport half still applies", func(t *testing.T) {
		got := byScenario[pointer("The transport half still applies to a name")]
		if !strings.Contains(got, `step uses "http", which describes the wire`) {
			t.Errorf("a transport word inside a sabotage object name was not flagged:\n%s", got)
		}
	})

	// The phrase exemption must not turn into an allowlist hole: the declared
	// object name never falls through to the glossary remedy, even now that the
	// fabricated one is reported by the phrase pass.
	if strings.Contains(joined, `step token "rides_status_code_present_check" is not declared`) {
		t.Errorf("the declared object name fell through to the glossary remedy:\n%s", joined)
	}
	// The reality sources are not themselves vocabulary: no step here says a SQL
	// word as a word, so no SQL-word finding may fire — in particular not on the
	// `check` fragment of the fabricated name, which is the half of the §8
	// exemption the gate must leave alone.
	if strings.Contains(joined, `step uses the SQL word`) {
		t.Errorf("the SQL half of the sabotage exemption was narrowed:\n%s", joined)
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
		byScenario[f.Subject] += f.Message + "\n"
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

// TestPinGlossaryRejectsExhaustiveConcatenation is fix K: fix I's concatenation
// check cuts the term in exactly ONE place, so `httpstatus` (http + status) and
// `statuscode` (status + code) are refused while a THREE-part concatenation
// walks straight past it: `httpstatuscode` (http + status + code) and
// `statuscodehttp` (status + code + http) are glossary-addable today, and the
// remedy message then launders them permanently. The rule is the exhaustive
// one: a term is a disguise when its whole text partitions into TWO OR MORE
// parts, every part denylisted transport vocabulary (denylistedTransportTokens
// ∪ statusNouns). The two-part forms keep their existing rejection, and a step
// that says one of the refused terms falls out of the allowlist with it.
func TestPinGlossaryRejectsExhaustiveConcatenation(t *testing.T) {
	repo := vocabularyPinnedRepo(t, `common:
  - the
game:
  - httpstatus
  - httpstatuscode
  - ride
  - statuscode
  - statuscodehttp
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`, `@game
Feature: A glossed concatenation of any length

  @rule:R-KERNEL-001
  Scenario: The ride
    Then the ride

  @rule:R-KERNEL-002
  Scenario: A three-part transport concatenation
    Then httpstatuscode

  @rule:R-KERNEL-003
  Scenario: A three-part transport concatenation, reversed
    Then statuscodehttp
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	glossaryFindings := describeFindings(c.findings)
	findings, _ := vocabularyFindings(t, repo)
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.Subject] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a) Every concatenation length is refused, and NO refused term enters the
	// allowlist. The reason's wording is deliberately not pinned beyond the
	// finding's fixed prefix: a fix may report a three-part partition
	// differently from the two-part `%q + %q` phrasing, and the existing
	// TestPinGlossaryRejectsConcatenatedTransport already owns the two-part
	// wording.
	for _, term := range []string{"httpstatus", "statuscode", "httpstatuscode", "statuscodehttp"} {
		t.Run("rejected: "+term, func(t *testing.T) {
			if !strings.Contains(glossaryFindings,
				`glossary term "`+term+`" in section "game" is not a legitimate step word:`) {
				t.Errorf("glossary term %q was accepted:\n%s", term, glossaryFindings)
			}
			if g.sections["game"][term] {
				t.Errorf("glossary term %q entered the allowlist despite being a concatenated disguise", term)
			}
		})
	}

	// (b) The allowlist hole is the point: a step that says a refused term must
	// report it, not pass silently because the glossary laundering it was
	// accepted. RED today for the three-part forms (accepted, so the step is
	// clean); GREEN today for the two-part ones, which fix I already refuses.
	for _, pin := range []struct {
		scenario string
		term     string
	}{
		{"A three-part transport concatenation", "httpstatuscode"},
		{"A three-part transport concatenation, reversed", "statuscodehttp"},
	} {
		t.Run("step: "+pin.term+" is not addable", func(t *testing.T) {
			got := byScenario[pointer(pin.scenario)]
			if !strings.Contains(got, `step token "`+pin.term+`" is not declared`) {
				t.Errorf("a step said %q, which the glossary may not declare, and it passed:\n%s",
					pin.term, got)
			}
		})
	}

	// (c) The fixture's own baseline: the words the feature needs stay quiet, so
	// the findings above belong to the concatenations alone.
	t.Run("baseline: the ride", func(t *testing.T) {
		if got := byScenario[pointer("The ride")]; got != "" {
			t.Errorf("the fixture's baseline step was flagged:\n%s", got)
		}
	})
}

// TestPinGlossaryRejectsTrailingOStatus is fix L: `disguisedStatusPattern` is
// EXACTLY three characters (`^[1-5][0-9o]{2}$`), so `2oo` is refused while
// `200o` — the same figure with a trailing `o` standing for nothing — is four
// characters, passes disguiseReason, and is glossary-addable today. The rule is
// the normalized one: strip trailing o/O characters, substitute the remaining
// o/O with 0, then require a bare three-digit HTTP-range figure. The
// three-character shapes keep their existing rejection.
func TestPinGlossaryRejectsTrailingOStatus(t *testing.T) {
	repo := vocabularyPinnedRepo(t, `common:
  - the
game:
  - "200o"
  - "2o0o"
  - "2oo"
  - ride
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`, `@game
Feature: A glossed trailing-o status

  @rule:R-KERNEL-001
  Scenario: The ride
    Then the ride
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	joined := describeFindings(c.findings)

	// (a) The trailing-o forms are disguised statuses and never enter the
	// allowlist. RED today: both are four characters, so the three-character
	// pattern does not see them.
	for _, term := range []string{"200o", "2o0o"} {
		t.Run("rejected: "+term, func(t *testing.T) {
			if !strings.Contains(joined,
				`glossary term "`+term+`" in section "game" is not a legitimate step word: it is a numeric status`) {
				t.Errorf("glossary term %q was accepted:\n%s", term, joined)
			}
			if g.sections["game"][term] {
				t.Errorf("glossary term %q entered the allowlist despite being a disguised status", term)
			}
		})
	}

	// (b) The guard: the three-character shape is refused as it always was, so
	// a normalized check cannot become a narrowing of the existing ban.
	t.Run("guard: 2oo", func(t *testing.T) {
		if !strings.Contains(joined,
			`glossary term "2oo" in section "game" is not a legitimate step word: it is a numeric status`) {
			t.Errorf("the glossary accepted the letterspelled status `2oo`:\n%s", joined)
		}
		if g.sections["game"]["2oo"] {
			t.Error("glossary term \"2oo\" entered the allowlist despite being a disguise")
		}
	})
}

// pinTrailingOGlossary declares only the words the fixture says on purpose and
// NONE of the figures under test: the numeric-status rule (not the allowlist)
// has to catch them, so the remedy must name the status ban and never "add it
// to the glossary".
const pinTrailingOGlossary = `common:
  - the
game:
  - answer
  - says
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinTrailingOStatusDeniedInStep is fix L on the step side: `2oo`, `2OO`,
// `4O4` and `2o0` are already denied as step tokens (three characters), while
// `200o` falls through the three-character pattern and reaches the glossary
// binding, which reports it as a mere undeclared word — inviting the author to
// add the disguise. After the fix every one of the five forms is a numeric
// status, and none of them is offered the glossary remedy.
func TestPinTrailingOStatusDeniedInStep(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinTrailingOGlossary, `@game
Feature: A letterspelled figure is a status however it is spelled

  @rule:R-KERNEL-001
  Scenario: A trailing-o figure
    Then the answer says 200o

  @rule:R-KERNEL-002
  Scenario: The three-character disguises
    Then the answer says 2oo
    And the answer says 4O4
    And the answer says 2o0

  @rule:R-KERNEL-003
  Scenario: An uppercase disguise
    Then the answer says 2OO
`)

	findings, _ := vocabularyFindings(t, repo)
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.Subject] += f.Message + "\n"
	}
	pointer := func(scenario string) string {
		return "docs/spec/features/game/pinned.feature::" + scenario
	}

	// (a) RED today: `200o` is not a step-token disguise, so the only finding is
	// the undeclared-word remedy — which is exactly the laundering path the
	// glossary rejection (TestPinGlossaryRejectsTrailingOStatus) has to close
	// from the other side.
	t.Run("status: a trailing-o figure", func(t *testing.T) {
		got := byScenario[pointer("A trailing-o figure")]
		if !strings.Contains(got, `step token "200o" is a numeric status`) {
			t.Errorf("`200o` was not a numeric status in a step:\n%s", got)
		}
		if strings.Contains(got, `step token "200o" is not declared`) {
			t.Errorf("findings offer the glossary remedy for a disguise:\n%s", got)
		}
	})

	// (b) The guards: the three-character shapes keep their existing rejection —
	// normalized matching must ADD the trailing-o forms, never narrow this set.
	t.Run("status: the three-character disguises", func(t *testing.T) {
		got := byScenario[pointer("The three-character disguises")]
		for _, token := range []string{"2oo", "4o4", "2o0"} {
			if !strings.Contains(got, `step token "`+token+`" is a numeric status`) {
				t.Errorf("step token %q was not a numeric status:\n%s", token, got)
			}
			if strings.Contains(got, `step token "`+token+`" is not declared`) {
				t.Errorf("findings offer the glossary remedy for %q:\n%s", token, got)
			}
		}
	})

	// (c) The tokenizer lowercases, so the uppercase respelling is the same
	// token: `2OO` reports as `2oo` and stays a status.
	t.Run("status: an uppercase disguise", func(t *testing.T) {
		got := byScenario[pointer("An uppercase disguise")]
		if !strings.Contains(got, `step token "2oo" is a numeric status`) {
			t.Errorf("`2OO` was not a numeric status in a step:\n%s", got)
		}
		if strings.Contains(got, "is not declared") {
			t.Errorf("findings offer the glossary remedy for an uppercase disguise:\n%s", got)
		}
	})
}

// pinDigitLeadingGlossary declares the five digit-leading words the features say
// as business facts. None of them is a lookalike of a three-digit HTTP figure,
// so all five stay addable — and that is the guard that keeps a normalized
// trailing-o check from swallowing `60s` or `7th`.
const pinDigitLeadingGlossary = `common:
  - after
  - on
  - the
game:
  - "1st"
  - "30min"
  - "3d"
  - "60s"
  - "7th"
  - attempt
  - finishes
  - player
  - waits
identity: []
schedule: []
ledger: []
kernel: []
platform: []
`

// TestPinDigitLeadingWordsStayAddable is the other half of fix L: the disguise
// check must refuse the letterspelled FIGURE without refusing every
// digit-leading word. `1st`, `30min`, `3d`, `60s` and `7th` are business facts:
// they are declared by the glossary (no finding) and a step may say them (no
// finding). GREEN today; it must stay green after the fix.
func TestPinDigitLeadingWordsStayAddable(t *testing.T) {
	repo := vocabularyPinnedRepo(t, pinDigitLeadingGlossary, `@game
Feature: A digit-leading word is not a status

  @rule:R-KERNEL-001
  Scenario: An ordinal, a duration and a rank
    Given the player finishes 1st after 30min
    And the player waits 60s on the 7th attempt
    And the player waits 3d
`)

	c := &checker{root: repo.root}
	g := c.loadGlossary()
	glossaryFindings := describeFindings(c.findings)
	findings, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	byScenario := map[string]string{}
	for _, f := range findings {
		byScenario[f.Subject] += f.Message + "\n"
	}

	// (a) Addable: no glossary finding, and the term is in the allowlist.
	for _, term := range []string{"1st", "30min", "3d", "60s", "7th"} {
		t.Run("addable: "+term, func(t *testing.T) {
			if strings.Contains(glossaryFindings, `glossary term "`+term+`"`) {
				t.Errorf("glossary term %q was refused as a disguised status:\n%s", term, glossaryFindings)
			}
			if !g.sections["game"][term] {
				t.Errorf("glossary term %q did not enter the allowlist:\n%s", term, glossaryFindings)
			}
		})
	}

	// (b) Usable: the step says all five and is clean.
	t.Run("step: an ordinal, a duration and a rank", func(t *testing.T) {
		got := byScenario["docs/spec/features/game/pinned.feature::An ordinal, a duration and a rank"]
		if got != "" {
			t.Errorf("digit-leading business words were flagged in a step:\n%s", got)
		}
		if strings.Contains(joined, `is a numeric status`) {
			t.Errorf("a digit-leading business word was a numeric status:\n%s", joined)
		}
	})
}
