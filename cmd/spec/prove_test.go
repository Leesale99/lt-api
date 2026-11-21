package main

// Tests for `spec prove`: the registry seeds the 30 database-authority rules,
// every mapped scenario resolves, the static vacuity verdict is the same rule
// policy 18 applies, and the DB gate fails closed.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	messages "github.com/cucumber/messages/go/v34"
)

func realCatalogAndRoot(t *testing.T) (Catalog, string) {
	t.Helper()
	root := filepath.Join("..", "..")
	catalog, err := loadCatalog(filepath.Join(root, "docs", "spec", "rules.yaml"))
	if err != nil {
		t.Fatalf("load the real catalog: %v", err)
	}
	return catalog, root
}

func TestProveRegistrySeedsTheThirtyRules(t *testing.T) {
	catalog, root := realCatalogAndRoot(t)
	registry, err := loadProveRegistry(filepath.Join(root, proveRegistryFile))
	if err != nil {
		t.Fatalf("load prove registry: %v", err)
	}

	want := []string{
		"R-SCHEDULE-003", "R-SCHEDULE-004", "R-SCHEDULE-005", "R-SCHEDULE-006",
		"R-SCHEDULE-007", "R-SCHEDULE-008", "R-SCHEDULE-009", "R-SCHEDULE-010",
		"R-SCHEDULE-011", "R-SCHEDULE-013", "R-SCHEDULE-014", "R-SCHEDULE-015",
		"R-SCHEDULE-016", "R-SCHEDULE-023", "R-SCHEDULE-024", "R-SCHEDULE-025",
		"R-SCHEDULE-029", "R-GAME-001", "R-GAME-002", "R-GAME-003", "R-GAME-004",
		"R-GAME-005", "R-GAME-014", "R-GAME-025", "R-GAME-035", "R-IDENTITY-006",
		"R-IDENTITY-011", "R-IDENTITY-013", "R-IDENTITY-014", "R-IDENTITY-020",
	}
	if len(registry) != len(want) {
		t.Fatalf("registry has %d entries, want %d", len(registry), len(want))
	}
	for _, id := range want {
		if _, ok := registry[id]; !ok {
			t.Errorf("registry has no entry for %s", id)
		}
	}

	index := (&checker{root: root}).indexFeatures()
	known := map[string]bool{}
	for _, rule := range catalog {
		known[rule.ID] = true
	}
	for id, entry := range registry {
		if !known[id] {
			t.Errorf("registry names unknown rule %s", id)
			continue
		}
		if len(entry.Scenarios) == 0 {
			t.Errorf("registry entry %s has no scenario (seed entries are verifiable)", id)
		}
		for _, pointer := range entry.Scenarios {
			ref, ok := index.byPointer[pointer]
			if !ok {
				t.Errorf("%s maps to a scenario that does not exist: %s", id, pointer)
				continue
			}
			if ref.Rule != id {
				t.Errorf("%s maps to %s, which is tagged %s", id, pointer, ref.Rule)
			}
		}
	}
}

func outcomeStep(text string) *messages.PickleStep {
	return &messages.PickleStep{Text: text, Type: messages.PickleStepType_OUTCOME}
}

func TestProveVacuityMatchesPolicy18(t *testing.T) {
	rule := baseRule()
	rule.Authority = "database"
	rule.Refusal = &Refusal{Code: "23505"}

	tests := []struct {
		name   string
		rule   Rule
		steps  []*messages.PickleStep
		reason string
	}{
		{
			name:  "observable refusal",
			rule:  rule,
			steps: []*messages.PickleStep{outcomeStep("the write is refused by things_check")},
		},
		{
			name:   "no Then at all",
			rule:   rule,
			steps:  []*messages.PickleStep{{Text: "the thing is written", Type: messages.PickleStepType_ACTION}},
			reason: "no Then outcome",
		},
		{
			name:   "vacuous Then",
			rule:   rule,
			steps:  []*messages.PickleStep{outcomeStep("the call succeeds")},
			reason: "asserts nothing observable",
		},
		{
			name:   "write allowed on a database-refusal rule",
			rule:   rule,
			steps:  []*messages.PickleStep{outcomeStep("the write is allowed")},
			reason: "claims database refusal",
		},
		{
			name: "write allowed on a rule that claims no refusal",
			rule: func() Rule {
				r := rule
				r.Refusal = nil
				return r
			}(),
			steps: []*messages.PickleStep{outcomeStep("the write is allowed")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenarios := map[string][]oracleScenario{
				"f.feature::s": {{ref: scenarioRef{Path: "f.feature", Name: "s", Rule: tt.rule.ID}, steps: tt.steps}},
			}
			reason := proveVacuity(tt.rule, []string{"f.feature::s"}, scenarios)
			if tt.reason == "" {
				if reason != "" {
					t.Errorf("unexpected VACUOUS verdict: %s", reason)
				}
				return
			}
			if !strings.Contains(reason, tt.reason) {
				t.Errorf("verdict = %q, want it to mention %q", reason, tt.reason)
			}
		})
	}
}

func TestProveFailsClosedWithoutDatabase(t *testing.T) {
	t.Setenv(proveDSNEnv, "")
	var stdout, stderr bytes.Buffer
	code := runProve([]string{
		"--rules", filepath.Join("..", "..", "docs", "spec", "rules.yaml"),
		"--rule", "R-SCHEDULE-003",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("prove without a DSN = %d, want 1", code)
	}
	output := stdout.String() + stderr.String()
	for _, want := range []string{"fails closed", "AUDIT_SKIP_DB does not apply"} {
		if !strings.Contains(output, want) {
			t.Errorf("prove output does not mention %q:\n%s", want, output)
		}
	}
}

func TestProveRejectsUnknownRule(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProve([]string{
		"--rules", filepath.Join("..", "..", "docs", "spec", "rules.yaml"),
		"--rule", "R-NOPE-999",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("prove with an unknown rule = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown rule id") {
		t.Errorf("prove output does not report the unknown rule:\n%s", stderr.String())
	}
}

func TestProveDerivesScopeFromDirtyTree(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "spec@example.test")
	run("config", "user.name", "Spec Test")
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/spec/rules.yaml", "- id: R-KERNEL-001\n  title: base\n")
	write("internal/kernel/thing/thing.go", "package thing\n\nfunc Do() {}\n")
	write("docs/spec/features/kernel/thing.feature",
		"@kernel\nFeature: a thing\n\n  @rule:R-KERNEL-002\n  Scenario: a scenario\n    Then the thing reads \"locked\"\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")

	// Dirty the tree: a Go annotation, a feature tag, and a catalog change.
	write("internal/kernel/thing/thing.go", "package thing\n\n//rule:R-KERNEL-003\nfunc Do() {}\n")
	write("docs/spec/features/kernel/thing.feature",
		"@kernel\nFeature: a thing\n\n  @rule:R-KERNEL-004\n  Scenario: a scenario\n    Then the thing reads \"locked\"\n")
	write("docs/spec/rules.yaml", "- id: R-KERNEL-001\n  title: changed\n")

	catalog := Catalog{{ID: "R-KERNEL-001", Title: "changed"}}
	index := (&checker{root: root}).indexFeatures()
	scope, err := deriveProveScope(root, "HEAD", filepath.Join(root, "docs/spec/rules.yaml"), "docs/spec/prove.yaml", catalog, index)
	if err != nil {
		t.Fatalf("derive scope: %v", err)
	}
	for _, want := range []string{"R-KERNEL-001", "R-KERNEL-003", "R-KERNEL-004"} {
		if !scope[want] {
			t.Errorf("derived scope does not contain %s: %v", want, scope)
		}
	}
}
