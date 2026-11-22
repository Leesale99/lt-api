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
// The first two are CATALOG-level findings here: their comment group names no
// known rule, so the subject column is `(catalog)` — never a real id — and the
// label that says which kind of mistake it is rides at the head of the message.
// The synthetic tree holds all three at once so the sorted grouping is exercised
// too.

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
	// The typo findings carry no rule subject: their comment group names no
	// known rule, so no scope could ever implicate them.
	for _, f := range findings {
		if f.Subject == "R-KERNEL-1" || f.Subject == "R-KERNEL-999" {
			t.Errorf("finding %q is attributed to the id it failed to resolve: %+v", f.Message, f)
		}
	}

	var report bytes.Buffer
	printFindings(&report, repo.rulesPath, catalog, findings, nil)
	rendered := report.String()

	malformed := "- **(catalog)** (malformed id) internal/kernel/thing/malformed.go:3 carries //rule:R-KERNEL-1, which is not an id"
	unknown := "- **(catalog)** (unknown id R-KERNEL-999) internal/kernel/thing/unknown.go:3 carries //rule:R-KERNEL-999, which names no rule in the catalog docs/spec/rules"
	real := "- **R-KERNEL-001**"
	for _, want := range []string{
		malformed,
		unknown,
		real,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}

	// A malformed id is not rendered as a bare rule id, and an unknown id is not
	// either: both carry their label at the head of the message.
	for _, subject := range []string{"- **R-KERNEL-1**", "- **R-KERNEL-999**"} {
		if strings.Contains(rendered, subject) {
			t.Errorf("report renders %q as if it were a catalog rule id:\n%s", subject, rendered)
		}
	}

	// The sorted grouping is intact: catalog-level labels sort ahead of the real
	// rule id, and the two labels keep their message order.
	if !(strings.Index(rendered, malformed) < strings.Index(rendered, unknown) &&
		strings.Index(rendered, unknown) < strings.Index(rendered, real)) {
		t.Errorf("policy-15 findings are not sorted by subject then message:\n%s", rendered)
	}
}
