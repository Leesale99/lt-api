package main

// Tests for checker policy 15 (`15-annotations`, annotations.go): every Go
// enforcement site an implemented rule names carries a `//rule:` annotation at
// its declaration, and every `//rule:` annotation in the tree resolves to a
// catalog rule — in both directions, plus the typo catch.
//
// Fixtures live under testdata/annotations: complete Go files whose declarations
// demonstrate the pinned convention (whole-line annotation stacked under the doc
// comment, one line per id) and its failure modes (no annotation, a blank line
// breaking contiguity, an unknown id, a malformed id).

import (
	"bytes"
	"strings"
	"testing"
)

// annotationRepo is a synthetic checkout whose thing.go is a testdata fixture.
// Fixtures carry a `.txt` suffix so the repository's own gofmt gate never
// rewrites them: a doc comment holding `//rule:` is normalized by gofmt to
// `// rule:`, and a fixture must be free to pin the canonical spelling.
func annotationRepo(t *testing.T, fixture string) *testRepo {
	t.Helper()

	repo := newTestRepo(t)
	repo.addFile(t, "internal/kernel/thing/thing.go", readTestdata(t, "annotations/"+fixture))
	return repo
}

// rulesNaming builds a catalog of implemented rules that all name one site.
func rulesNaming(site string, ids ...string) Catalog {
	catalog := make(Catalog, 0, len(ids))
	for _, id := range ids {
		rule := baseRule()
		rule.ID = id
		rule.Enforcement = []Enforcement{{Site: site, Layer: "service", Role: "authoritative"}}
		catalog = append(catalog, rule)
	}
	return catalog
}

func annotationMessages(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.RuleID + " :: " + f.Message + "\n")
	}
	return b.String()
}

func TestAnnotationsMissingAtEnforcementSite(t *testing.T) {
	repo := annotationRepo(t, "missing-annotation.go.txt")

	findings := collectAnnotations(baseCatalog(), repo.root)
	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1:\n%s", len(findings), describeFindings(findings))
	}
	f := findings[0]
	if f.Policy != policyAnnotations || f.Severity != severityError || f.RuleID != "R-KERNEL-001" {
		t.Fatalf("finding = %+v, want an error attributed to R-KERNEL-001 under %s", f, policyAnnotations)
	}
	for _, want := range []string{
		`carries no //rule:R-KERNEL-001 annotation`,
		"internal/kernel/thing/thing.go::Do",
	} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not mention %q", f.Message, want)
		}
	}
}

func TestAnnotationsRequireEveryRuleNamingOneSite(t *testing.T) {
	repo := annotationRepo(t, "one-of-two.go.txt")

	catalog := rulesNaming("internal/kernel/thing/thing.go::Do", "R-KERNEL-001", "R-KERNEL-002")
	findings := collectAnnotations(catalog, repo.root)

	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1 (the missing second id):\n%s", len(findings), describeFindings(findings))
	}
	if findings[0].RuleID != "R-KERNEL-002" || !strings.Contains(findings[0].Message, "no //rule:R-KERNEL-002") {
		t.Errorf("finding = %+v, want the missing R-KERNEL-002 annotation", findings[0])
	}
}

func TestAnnotationsBlankLineBreaksContiguity(t *testing.T) {
	repo := annotationRepo(t, "broken-contiguity.go.txt")

	findings := collectAnnotations(baseCatalog(), repo.root)
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "carries no //rule:R-KERNEL-001") {
		t.Fatalf("%d findings, want the missing annotation:\n%s", len(findings), describeFindings(findings))
	}
}

func TestAnnotationsAcceptTrailingComment(t *testing.T) {
	repo := annotationRepo(t, "trailing-annotation.go.txt")

	if findings := collectAnnotations(baseCatalog(), repo.root); len(findings) != 0 {
		t.Fatalf("a trailing annotation on the declaration line was rejected:\n%s", describeFindings(findings))
	}
}

func TestAnnotationsAcceptStackedAnnotationsUnderDocComment(t *testing.T) {
	repo := annotationRepo(t, "stacked-method.go.txt")

	catalog := rulesNaming("internal/kernel/thing/thing.go::Handler.Do", "R-KERNEL-001", "R-KERNEL-002")
	if findings := collectAnnotations(catalog, repo.root); len(findings) != 0 {
		t.Fatalf("stacked annotations above a method declaration were rejected:\n%s", describeFindings(findings))
	}
}

func TestAnnotationsResolveToCatalogRules(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{
			name:    "unknown id",
			fixture: "unknown-annotation.go.txt",
			want:    "//rule:R-KERNEL-999, which names no rule in the catalog",
		},
		{
			name:    "malformed id",
			fixture: "malformed-annotation.go.txt",
			want:    "//rule:R-KERNEL-1, which is not an id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := annotationRepo(t, tt.fixture)

			findings := collectAnnotations(baseCatalog(), repo.root)
			if len(findings) != 1 {
				t.Fatalf("%d findings, want 1:\n%s", len(findings), describeFindings(findings))
			}
			if findings[0].Policy != policyAnnotations || findings[0].Severity != severityError {
				t.Fatalf("finding = %+v, want an error under %s", findings[0], policyAnnotations)
			}
			if !strings.Contains(findings[0].Message, tt.want) {
				t.Errorf("message %q does not mention %q", findings[0].Message, tt.want)
			}
			if !strings.Contains(findings[0].Message, "internal/kernel/thing/thing.go:") {
				t.Errorf("message %q does not name file:line", findings[0].Message)
			}
		})
	}
}

func TestAnnotationsExemptSQLAndTestSites(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "migrations/000001_add_things.up.sql", "-- a thing\n")
	repo.addFile(t, "internal/kernel/thing/thing_test.go",
		"package thing\n\nimport \"testing\"\n\nfunc TestDo(t *testing.T) {}\n")

	rule := baseRule()
	rule.Enforcement = []Enforcement{
		{Site: "migrations/000001_add_things.up.sql", Layer: "db-constraint", Role: "authoritative"},
		{Site: "internal/kernel/thing/thing_test.go::TestDo", Layer: "repository", Role: "defense-in-depth", PinnedBy: ptr("shared")},
	}
	rule.Fixture = ptr("shared")

	findings := collectAnnotations(Catalog{rule}, repo.root)
	// thing.go (seeded with the annotation) is not named by this rule, so the
	// only possible findings would be the exempt sites.
	if len(findings) != 0 {
		t.Fatalf("SQL and test sites were required to carry annotations:\n%s", annotationMessages(findings))
	}
}

func TestAnnotationsSkipVendorAndTestdata(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "vendor/thirdparty/thing.go", "package thirdparty\n\n//rule:NOT-A-RULE\nfunc Do() {}\n")
	repo.addFile(t, "internal/kernel/thing/testdata/fixture.go", "package thing\n\n//rule:R-KERNEL-999\nfunc Do() {}\n")

	if findings := collectAnnotations(baseCatalog(), repo.root); len(findings) != 0 {
		t.Fatalf("vendor/testdata Go sources were scanned:\n%s", describeFindings(findings))
	}
}

// TestAnnotationsIgnoreProseAndStringLiterals pins the parser-based read: a
// `//rule:` mentioned mid-sentence or inside a string is not an annotation, so
// it neither satisfies the site requirement nor creates a reverse finding.
func TestAnnotationsIgnoreProseAndStringLiterals(t *testing.T) {
	repo := annotationRepo(t, "prose-annotation.go.txt")

	findings := collectAnnotations(baseCatalog(), repo.root)
	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1 (the site is still unannotated):\n%s",
			len(findings), describeFindings(findings))
	}
	if !strings.Contains(findings[0].Message, "carries no //rule:R-KERNEL-001 annotation") {
		t.Errorf("finding = %q, want the missing-annotation error", findings[0].Message)
	}
}

// TestAnnotationsAcceptGofmtNormalizedSpelling keeps the gate satisfiable under
// `gofmt`: the formatter reflows a doc comment's `//rule:` to `// rule:`, and
// that is the same annotation.
func TestAnnotationsAcceptGofmtNormalizedSpelling(t *testing.T) {
	repo := annotationRepo(t, "gofmt-normalized.go.txt")

	if findings := collectAnnotations(baseCatalog(), repo.root); len(findings) != 0 {
		t.Fatalf("gofmt's normalized annotation spelling was rejected:\n%s", describeFindings(findings))
	}
}

func TestAnnotationsTestFilesAreCheckedInReverse(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "internal/kernel/thing/thing_test.go",
		"package thing\n\nimport \"testing\"\n\n//rule:R-KERNEL-404\nfunc TestDo(t *testing.T) {}\n")

	findings := collectAnnotations(baseCatalog(), repo.root)
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "names no rule") {
		t.Fatalf("%d findings, want the unresolvable annotation in a test file:\n%s",
			len(findings), describeFindings(findings))
	}
}

func TestPrintFindingsReportsAnnotationPolicy(t *testing.T) {
	repo := annotationRepo(t, "missing-annotation.go.txt")
	catalog := Catalog{gherkinRule("R-KERNEL-001")}

	findings := collectAnnotations(catalog, repo.root)
	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, findings, nil)
	rendered := report.String()

	for _, want := range []string{
		"## " + policyAnnotations + " — Go enforcement sites carry //rule: annotations, and every annotation resolves",
		"| `" + policyAnnotations + "` | 1 | 0 |",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}
}
