package main

// Tests for checker policy 13 (`13-vocabulary`, vocabulary.go): the denylist is
// absolute, the seeded glossary binds every other token, and the sabotage step
// family keeps its SQL verbs.
//
// Fixtures live under testdata/features (a synthetic checkout's
// docs/spec/features tree) with testdata/glossary.yaml as its allowlist; the
// last test runs the policy over the real feature tree, so the seeded glossary
// and today's Gherkin cannot drift apart.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// vocabularyRepo is a synthetic checkout: the named testdata features plus the
// fixture glossary at docs/spec/glossary.yaml.
func vocabularyRepo(t *testing.T, features ...string) *testRepo {
	t.Helper()

	repo := newTestRepo(t)
	repo.addFile(t, "docs/spec/glossary.yaml", readTestdata(t, "glossary.yaml"))
	for _, feature := range features {
		repo.addFile(t, "docs/spec/features/"+strings.TrimPrefix(feature, "features/"),
			readTestdata(t, feature))
	}
	return repo
}

// vocabularyFindings runs policy 13 over a synthetic checkout and returns the
// findings plus their messages in order.
func vocabularyFindings(t *testing.T, repo *testRepo) ([]Finding, []string) {
	t.Helper()

	findings := collectVocabulary(repo.root)
	messages := make([]string, 0, len(findings))
	for _, f := range findings {
		messages = append(messages, f.RuleID+" :: "+f.Message)
	}
	sort.Strings(messages)
	return findings, messages
}

func TestTokenizeStep(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "lowercases and drops punctuation",
			text: "Then the ride is locked.",
			want: []string{"then", "the", "ride", "is", "locked"},
		},
		{
			name: "quoted literals are data, not vocabulary",
			text: `the teams "Team A" and "Team B" play in "Spring 2026"`,
			want: []string{"the", "teams", "and", "play", "in"},
		},
		{
			name: "possessives fold away",
			text: `it is 3 hours before round 1's first match`,
			want: []string{"it", "is", "3", "hours", "before", "round", "1", "first", "match"},
		},
		{
			name: "a quoted literal followed by a possessive leaves nothing",
			text: `the second member changes "Ana"'s registration`,
			want: []string{"the", "second", "member", "changes", "registration"},
		},
		{
			name: "en dashes split scores",
			text: `match day 1 ends "2–1" to "Team A"`,
			want: []string{"match", "day", "1", "ends", "to"},
		},
		{
			name: "hyphens split, underscores do not",
			text: `the organiser re-reads the team; rides_state_gate refuses`,
			want: []string{"the", "organiser", "re", "reads", "the", "team", "rides_state_gate", "refuses"},
		},
		{
			name: "scenario outline placeholders are prose",
			text: `the Owner pool tries to insert a ride in the state "<state>"`,
			want: []string{"the", "owner", "pool", "tries", "to", "insert", "a", "ride", "in", "the", "state"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeStep(tc.text)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("tokenizeStep(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestVocabularyReportsDeniedAndUndeclaredTokens(t *testing.T) {
	repo := vocabularyRepo(t, "features/game/vocabulary-denied.feature")

	findings, messages := vocabularyFindings(t, repo)

	want := []string{
		`step uses "json", which describes the wire`,
		`step uses "request", which describes the wire`,
		`step uses "status code": numeric statuses and transport vocabulary are forbidden`,
		`step carries the bare number "409"`,
		`step token "scheduler" is not declared for module "game"`,
		`step token "ponders" is not declared for module "game"`,
	}
	if len(messages) != len(want) {
		t.Fatalf("%d findings, want %d:\n%s", len(messages), len(want), strings.Join(messages, "\n"))
	}
	for _, fragment := range want {
		if !strings.Contains(strings.Join(messages, "\n"), fragment) {
			t.Errorf("findings do not mention %q:\n%s", fragment, strings.Join(messages, "\n"))
		}
	}

	// Attribution: every finding names the scenario the way a
	// verification.gherkin pointer does. A denied token is reported once (the
	// denylist finding), not twice (it is also undeclared).
	for _, f := range findings {
		if f.Policy != policyVocabulary {
			t.Errorf("finding carries policy %q, want %q", f.Policy, policyVocabulary)
		}
		if f.Severity != severityError {
			t.Errorf("finding %q is not an error", f.Message)
		}
		if !strings.Contains(f.RuleID, "docs/spec/features/game/vocabulary-denied.feature::") {
			t.Errorf("finding %q does not name file::scenario (got %q)", f.Message, f.RuleID)
		}
	}
}

func TestVocabularySabotageStepsKeepSQLVerbs(t *testing.T) {
	repo := vocabularyRepo(t, "features/game/vocabulary-sabotage.feature")

	_, messages := vocabularyFindings(t, repo)

	// `insert` and `write` are SQL/administrative words the §8 family says on
	// purpose; the transport words stay forbidden there.
	for _, message := range messages {
		if strings.Contains(message, `"insert"`) || strings.Contains(message, `"write"`) {
			t.Errorf("a sabotage step was flagged for SQL vocabulary: %s", message)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("%d findings, want 2 (json, payload):\n%s", len(messages), strings.Join(messages, "\n"))
	}
	for _, want := range []string{`step uses "json"`, `step uses "payload"`} {
		if !strings.Contains(strings.Join(messages, "\n"), want) {
			t.Errorf("findings do not mention %q:\n%s", want, strings.Join(messages, "\n"))
		}
	}
}

func TestVocabularySQLWordsDeniedOutsideSabotage(t *testing.T) {
	repo := vocabularyRepo(t, "features/game/vocabulary-facade.feature")

	_, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")

	// The exact keyword is denied in a facade step...
	if !strings.Contains(joined, `step uses the SQL word "update"`) {
		t.Errorf("findings do not report the SQL keyword:\n%s", joined)
	}
	// ...and only the exact keyword: `selects` is a domain verb.
	if strings.Contains(joined, `step uses the SQL word "selects"`) {
		t.Errorf("the denylist matched a conjugated verb:\n%s", joined)
	}
	if len(messages) != 1 {
		t.Fatalf("%d findings, want 1 (update):\n%s", len(messages), joined)
	}
}

func TestVocabularyFeatureOutsideAModule(t *testing.T) {
	repo := vocabularyRepo(t, "features/nowhere/vocabulary.feature")

	_, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, `is not under docs/spec/features/<module>/`) {
		t.Errorf("findings do not report the unplaceable feature:\n%s", joined)
	}
}

func TestVocabularyMissingGlossary(t *testing.T) {
	repo := newTestRepo(t)
	repo.removeFile(t, "docs/spec/glossary.yaml")
	repo.addFile(t, "docs/spec/features/game/vocabulary-sabotage.feature",
		readTestdata(t, "features/game/vocabulary-sabotage.feature"))

	_, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, `glossary "docs/spec/glossary.yaml" cannot be read`) {
		t.Errorf("findings do not report the missing glossary:\n%s", joined)
	}
	// The red run still points at a scenario, so the fix is actionable.
	if !strings.Contains(joined, "docs/spec/features/game/vocabulary-sabotage.feature::") {
		t.Errorf("findings do not attribute undeclared tokens to a scenario:\n%s", joined)
	}
}

func TestVocabularyRequiresEveryModuleSection(t *testing.T) {
	repo := vocabularyRepo(t)
	repo.addFile(t, "docs/spec/glossary.yaml", "common:\n  - the\ngame:\n  - ride\n")

	_, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	for _, module := range []string{"identity", "schedule", "ledger", "kernel", "platform"} {
		want := `glossary has no "` + module + `" section`
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not mention %q:\n%s", want, joined)
		}
	}
}

func TestVocabularyRejectsMalformedGlossaryTerms(t *testing.T) {
	repo := vocabularyRepo(t)
	repo.addFile(t, "docs/spec/glossary.yaml",
		"common:\n  - The\nkernel:\n  - ride-lock\nidentity: []\nschedule: []\nledger: []\nplatform: []\ngame: []\n")

	_, messages := vocabularyFindings(t, repo)
	joined := strings.Join(messages, "\n")
	for _, want := range []string{
		`glossary term "The" in section "common" is not a single lowercase token`,
		`glossary term "ride-lock" in section "kernel" is not a single lowercase token`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not mention %q:\n%s", want, joined)
		}
	}
}

// TestVocabularyRealTreePassesSeededGlossary is the gate's half of policy 13:
// every step token in docs/spec/features/** is either in its module's glossary
// section, in `common`, or denied — so the seeded glossary and today's tree
// agree.
func TestVocabularyRealTreePassesSeededGlossary(t *testing.T) {
	root := filepath.Join("..", "..")

	findings := collectVocabulary(root)
	if len(findings) != 0 {
		t.Fatalf("the seeded glossary does not cover the real feature tree:\n%s",
			describeFindings(findings))
	}
}

// TestVocabularyRealGlossaryDeclaresEveryModule keeps the seed's shape honest:
// `common` plus one section per module enum value, no more, no less.
func TestVocabularyRealGlossaryDeclaresEveryModule(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "docs/spec/glossary.yaml"))
	if err != nil {
		t.Fatalf("read the seeded glossary: %v", err)
	}

	g := &checker{root: root}
	glossary := g.loadGlossary()
	for _, f := range g.findings {
		t.Errorf("the seeded glossary reports %q: %s", f.Policy, f.Message)
	}
	if _, ok := glossary.sections[glossaryCommonSection]; !ok {
		t.Errorf("the seeded glossary has no %q section", glossaryCommonSection)
	}
	for module := range moduleEnum {
		if _, ok := glossary.sections[module]; !ok {
			t.Errorf("the seeded glossary has no %q section", module)
		}
	}
	for _, module := range []string{"game", "identity", "schedule"} {
		if len(glossary.sections[module]) == 0 {
			t.Errorf("section %q is empty but the tree has %s features", module, module)
		}
	}
	if len(data) == 0 {
		t.Error("the seeded glossary is empty")
	}
}

func TestPrintFindingsReportsVocabularyPolicy(t *testing.T) {
	repo := vocabularyRepo(t, "features/game/vocabulary-denied.feature")
	catalog := Catalog{gherkinRule("R-KERNEL-001")}

	findings := collectVocabulary(repo.root)
	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, findings, nil)
	rendered := report.String()

	for _, want := range []string{
		"## " + policyVocabulary + " — step text: denylisted transport/SQL tokens and the seeded glossary",
		"| `" + policyVocabulary + "` | 6 | 0 |",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}
}
