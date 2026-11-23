package main

// Tests pinning policy 15's fail-closed read of a required site whose file the
// scanner never reads. scanGoFiles deliberately skips vendor, testdata and
// hidden directories, so a required Go site living in one of them is invisible
// to the forward direction while policy 3 still accepts it (the file exists on
// disk). That combination used to be a silent pass; it is now an ERROR naming
// the skipped directory. Only a file that genuinely does not exist stays
// silent here, because policy 3 owns that finding.

import (
	"strings"
	"testing"
)

// TestAnnotationsRequiredSiteInSkippedDirectoryFailsClosed pins the silent-pass
// fix: the site's file exists (and even carries the annotation), but it sits in
// a directory the scanner skips, so the annotation can never be verified.
func TestAnnotationsRequiredSiteInSkippedDirectoryFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{name: "testdata", dir: "testdata"},
		{name: "vendor", dir: "vendor"},
		{name: "hidden dot-directory", dir: ".hidden"},
		{name: "nested skipped directory", dir: "internal/kernel/thing/testdata"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			site := tt.dir + "/fixture/thing.go"
			repo.addFile(t, site, "package thing\n\n//rule:R-KERNEL-001\nfunc Do() {}\n")

			rule := baseRule()
			rule.Enforcement = []Enforcement{{Site: site + "::Do", Layer: "service", Role: "authoritative"}}

			findings := collectAnnotations(Catalog{rule}, repo.root)
			if len(findings) != 1 {
				t.Fatalf("%d findings, want 1 (a required site in a skipped directory is never a silent pass):\n%s",
					len(findings), describeFindings(findings))
			}
			f := findings[0]
			if f.Policy != policyAnnotations || f.Severity != severityError || f.Subject != "R-KERNEL-001" {
				t.Fatalf("finding = %+v, want an error attributed to R-KERNEL-001 under %s", f, policyAnnotations)
			}
			skippedDir := tt.dir[strings.LastIndex(tt.dir, "/")+1:]
			for _, want := range []string{
				"skipped directory",
				`"` + skippedDir + `"`,
				site,
			} {
				if !strings.Contains(f.Message, want) {
					t.Errorf("message %q does not mention %q", f.Message, want)
				}
			}
		})
	}
}

// TestAnnotationsMissingSymbolStillFailsClosed is the already-green guard: a
// site in a scanned directory whose symbol no declaration carries keeps its
// own ERROR, so the skipped-directory branch did not swallow it.
func TestAnnotationsMissingSymbolStillFailsClosed(t *testing.T) {
	repo := newTestRepo(t)

	rule := baseRule()
	rule.Enforcement = []Enforcement{{
		Site:  "internal/kernel/thing/thing.go::Absent",
		Layer: "service", Role: "authoritative",
	}}

	findings := collectAnnotations(Catalog{rule}, repo.root)
	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1 (the missing symbol):\n%s", len(findings), describeFindings(findings))
	}
	if !strings.Contains(findings[0].Message, "which no declaration in the file carries") {
		t.Errorf("finding = %q, want the missing-symbol error", findings[0].Message)
	}
	if strings.Contains(findings[0].Message, "skipped directory") {
		t.Errorf("finding = %q, want no skipped-directory claim for a scanned file", findings[0].Message)
	}
}

// TestAnnotationsExemptNonGoSiteProducesNoSkipError is the already-green guard
// for the exemption: a non-Go site (a migration) flows through no annotation
// requirement at all, so no skipped-directory ERROR is invented for it.
func TestAnnotationsExemptNonGoSiteProducesNoSkipError(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "vendor/fixture/000001_thing.up.sql", "-- a thing\n")

	for _, site := range []string{
		"migrations/000001_add_things.up.sql",
		"migrations/000001_add_things.up.sql::anything",
		"vendor/fixture/000001_thing.up.sql",
	} {
		rule := baseRule()
		rule.Enforcement = []Enforcement{{Site: site, Layer: "db-constraint", Role: "authoritative"}}

		if findings := collectAnnotations(Catalog{rule}, repo.root); len(findings) != 0 {
			t.Fatalf("non-Go site %q produced findings:\n%s", site, describeFindings(findings))
		}
	}
}

// TestAnnotationsAbsentFileStaysSilent is the already-green guard for the case
// policy 3 owns: the file is not on disk at all, so policy 15 says nothing.
func TestAnnotationsAbsentFileStaysSilent(t *testing.T) {
	repo := newTestRepo(t)

	rule := baseRule()
	rule.Enforcement = []Enforcement{{
		Site:  "internal/kernel/thing/gone.go::Do",
		Layer: "service", Role: "authoritative",
	}}

	if findings := collectAnnotations(Catalog{rule}, repo.root); len(findings) != 0 {
		t.Fatalf("a genuinely missing file produced findings (policy 3 owns it):\n%s", describeFindings(findings))
	}
}
