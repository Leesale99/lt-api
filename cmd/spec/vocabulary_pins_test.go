package main

// Red-first pins for the policy 13 vocabulary hardening. One test per fix,
// each labelled with the fix it belongs to:
//
//	TestPinnedDenylistSurvivesUnderscores        — fix A (denylist over the `_`-split stream)
//	TestPinnedGlossaryRejectsDisguisedTerms      — fix B (the glossary may not launder a disguise)
//	TestPinnedBareThreeDigitQuantityOrStatus     — fix C (a three-digit figure, quantity vs status)
//	TestPinnedDocStringAndTableCellVocabulary    — fix D (docstrings and data-table cells are scanned)
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
// the HTTP range is a QUANTITY when the next token is a plain alphabetic word
// that is not a transport noun ("200 entries"), and a numeric STATUS at the end
// of a step or before a transport noun ("refused as 409", "409 status"). The
// quantityUnits guard ("100 tokens") keeps working.
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
