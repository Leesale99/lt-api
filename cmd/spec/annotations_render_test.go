package main

// Tests for the rendering of policy-15 findings (annotations.go
// checkAnnotationsResolve, check.go printFindings). Three conditions must be
// distinguishable in the report at a glance:
//
//   - a malformed annotation id (not `^R-[A-Z]+-\d{3}$`) — labeled, never
//     presented as if it were a rule id;
//   - a well-formed id that names no catalog rule — labeled with the id and the
//     catalog it failed to resolve against;
//   - an ordinary finding about a real catalog rule id — the bare id.
//
// The synthetic tree holds all three at once so the sorted grouping is
// exercised too.

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintFindingsDistinguishesAnnotationIdKinds(t *testing.T) {
	repo := newTestRepo(t)
	// Real rule id: the site exists but carries no annotation.
	repo.addFile(t, "internal/kernel/thing/thing.go",
		"package thing\n\n// Do does the thing.\nfunc Do() {}\n")
	// Malformed id: two digits.
	repo.addFile(t, "internal/kernel/thing/malformed.go",
		"package thing\n\n//rule:R-KERNEL-1\nfunc Malformed() {}\n")
	// Well-formed id the catalog never issued.
	repo.addFile(t, "internal/kernel/thing/unknown.go",
		"package thing\n\n//rule:R-KERNEL-999\nfunc Unknown() {}\n")

	catalog := baseCatalog()
	findings := collectAnnotations(catalog, repo.root)
	sortFindings(findings)
	if len(findings) != 3 {
		t.Fatalf("%d findings, want 3 (missing annotation, malformed id, unknown id):\n%s",
			len(findings), describeFindings(findings))
	}

	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, findings, nil)
	rendered := report.String()

	malformed := "- **(malformed id)**"
	unknown := "- **(unknown id R-KERNEL-999)**"
	real := "- **R-KERNEL-001**"
	for _, want := range []string{
		malformed,
		unknown,
		real,
		"names no rule in the catalog docs/spec/rules",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}

	// A malformed id is not rendered as a bare rule id, and an unknown id is not
	// either: both carry their label in the subject column.
	for _, subject := range []string{"- **R-KERNEL-1**", "- **R-KERNEL-999**"} {
		if strings.Contains(rendered, subject) {
			t.Errorf("report renders %q as if it were a catalog rule id:\n%s", subject, rendered)
		}
	}

	// The sorted grouping is intact: labels and the real id sort in one policy
	// section, each subject appearing once.
	if !(strings.Index(rendered, malformed) < strings.Index(rendered, unknown) &&
		strings.Index(rendered, unknown) < strings.Index(rendered, real)) {
		t.Errorf("policy-15 findings are not sorted by their rendered subject:\n%s", rendered)
	}
}
