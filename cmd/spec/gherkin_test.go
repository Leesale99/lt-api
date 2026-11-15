package main

// Tests for the feature cross-validation (gherkin.go): the pointers resolve in
// both directions, Scenario Outline rows interpolate, and a broken feature tree
// is reported instead of silently skipped.
//
// Fixtures live under testdata/features (a synthetic checkout's
// docs/spec/features tree); the catalog is built in the test, never read from
// the real one.

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// gherkinRepo is a synthetic checkout carrying the testdata feature tree plus a
// catalog the caller builds.
func gherkinRepo(t *testing.T, features ...string) *testRepo {
	t.Helper()

	repo := newTestRepo(t)
	for _, feature := range features {
		data := readTestdata(t, feature)
		repo.addFile(t, "docs/spec/features/"+strings.TrimPrefix(feature, "features/"), data)
	}
	return repo
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func gherkinRule(id string, pointers ...string) Rule {
	rule := baseRule()
	rule.ID = id
	rule.Verification.Gherkin = pointers
	return rule
}

func TestCollectGherkinResolvesPointersAndTags(t *testing.T) {
	repo := gherkinRepo(t,
		"features/happy/ride.feature",
		"features/outline/interpolated.feature",
	)

	catalog := Catalog{
		gherkinRule("R-KERNEL-001",
			"docs/spec/features/happy/ride.feature::A new ride is born locked",
			"docs/spec/features/happy/ride.feature::A ride cannot be born in any other state"),
		gherkinRule("R-KERNEL-002",
			"docs/spec/features/happy/ride.feature::A lost match ends the ride"),
		gherkinRule("R-KERNEL-003",
			"docs/spec/features/outline/interpolated.feature::The alpha claim",
			"docs/spec/features/outline/interpolated.feature::The beta claim"),
	}

	findings, index := collectGherkin(catalog, repo.root)
	if len(findings) != 0 {
		t.Fatalf("resolvable features produced findings:\n%s", describeFindings(findings))
	}

	tests := []struct {
		rule string
		want int
	}{
		{"R-KERNEL-001", 2}, // the group tag covers both scenarios
		{"R-KERNEL-002", 1},
		{"R-KERNEL-003", 2}, // one per interpolated outline row
	}
	for _, tc := range tests {
		if got := len(index.byRule[tc.rule]); got != tc.want {
			t.Errorf("%s: %d scenarios, want %d", tc.rule, got, tc.want)
		}
	}

	for _, file := range index.files {
		if file.Path == "docs/spec/features/happy/ride.feature" && file.Scenarios != 3 {
			t.Errorf("happy/ride.feature: %d scenarios, want 3", file.Scenarios)
		}
	}
}

func TestCollectGherkinReports(t *testing.T) {
	tests := []struct {
		name      string
		features  []string
		catalog   Catalog
		wantCount int
		wantIn    []string
	}{
		{
			name:     "unknown rule tag",
			features: []string{"features/broken/unknown-rule.feature"},
			catalog:  Catalog{gherkinRule("R-KERNEL-001")},
			wantIn: []string{
				`tagged with an unknown rule id`,
			},
		},
		{
			name:     "scenario without a rule tag",
			features: []string{"features/broken/untagged.feature"},
			catalog:  Catalog{gherkinRule("R-KERNEL-001")},
			wantIn: []string{
				`carries no @rule tag`,
			},
		},
		{
			name:     "unparseable feature",
			features: []string{"features/broken/unparseable.feature"},
			catalog:  Catalog{gherkinRule("R-KERNEL-001")},
			wantIn: []string{
				`does not parse`,
			},
		},
		{
			name:     "dangling pointer to a scenario",
			features: []string{"features/happy/ride.feature"},
			catalog: Catalog{gherkinRule("R-KERNEL-001",
				"docs/spec/features/happy/ride.feature::A scenario nobody wrote")},
			wantIn: []string{
				`names no scenario in docs/spec/features/happy/ride.feature`,
			},
		},
		{
			name:     "dangling pointer to a feature file",
			features: []string{"features/happy/ride.feature"},
			catalog: Catalog{gherkinRule("R-KERNEL-001",
				"docs/spec/features/game/nope.feature::A scenario")},
			wantIn: []string{
				`names no feature file with scenarios`,
			},
		},
		{
			name:     "pointer is not path::scenario",
			features: []string{"features/happy/ride.feature"},
			catalog: Catalog{gherkinRule("R-KERNEL-001",
				"docs/spec/features/happy/ride.feature")},
			wantIn: []string{
				`is not <feature-file-path>::<scenario name>`,
			},
		},
		{
			name:     "outline template name does not resolve",
			features: []string{"features/outline/interpolated.feature"},
			catalog: Catalog{gherkinRule("R-KERNEL-003",
				"docs/spec/features/outline/interpolated.feature::The <case> claim")},
			wantIn: []string{
				`names no scenario`,
			},
		},
		{
			name:     "pointer into an unparseable feature",
			features: []string{"features/broken/unparseable.feature"},
			catalog: Catalog{gherkinRule("R-KERNEL-001",
				"docs/spec/features/broken/unparseable.feature::A scenario")},
			wantIn: []string{
				`does not parse`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := gherkinRepo(t, tc.features...)
			findings, _ := collectGherkin(tc.catalog, repo.root)
			if len(findings) == 0 {
				t.Fatalf("no findings, want at least one")
			}
			described := describeFindings(findings)
			for _, want := range tc.wantIn {
				if !strings.Contains(described, want) {
					t.Errorf("findings do not mention %q:\n%s", want, described)
				}
			}
			errorsFound, _ := countFindings(findings)
			if errorsFound == 0 {
				t.Errorf("findings carry no error severity:\n%s", described)
			}
		})
	}
}

func TestPrintGherkinSectionReportsCoverage(t *testing.T) {
	repo := gherkinRepo(t, "features/happy/ride.feature")

	catalog := Catalog{
		gherkinRule("R-KERNEL-001",
			"docs/spec/features/happy/ride.feature::A new ride is born locked"),
		gherkinRule("R-KERNEL-002",
			"docs/spec/features/happy/ride.feature::A lost match ends the ride"),
	}
	gherkinFindings, index := collectGherkin(catalog, repo.root)
	if len(gherkinFindings) != 0 {
		t.Fatalf("unexpected findings:\n%s", describeFindings(gherkinFindings))
	}

	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, gherkinFindings, index)
	rendered := report.String()

	for _, want := range []string{
		"## gherkin — feature cross-validation",
		"| `docs/spec/features/happy/ride.feature` | 3 | R-KERNEL-001, R-KERNEL-002 |",
		"| `R-KERNEL-001` | 2 |",
		"| `R-KERNEL-002` | 1 |",
		"cross-validation: every pointer and tag resolves",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}
}

func TestPrintGherkinSectionListsErrors(t *testing.T) {
	repo := gherkinRepo(t, "features/broken/untagged.feature")
	catalog := Catalog{gherkinRule("R-KERNEL-001")}

	gherkinFindings, index := collectGherkin(catalog, repo.root)
	if len(gherkinFindings) == 0 {
		t.Fatal("no findings for an untagged scenario")
	}

	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, gherkinFindings, index)
	rendered := report.String()

	if !strings.Contains(rendered, "carries no @rule tag") {
		t.Errorf("the gherkin section does not list the finding:\n%s", rendered)
	}
	if strings.Contains(rendered, "## 12-features") {
		t.Errorf("the policy section duplicates the gherkin section:\n%s", rendered)
	}
	if !strings.Contains(rendered, "| `12-features` | 1 | 0 |") {
		t.Errorf("the summary does not count the feature finding:\n%s", rendered)
	}
}
