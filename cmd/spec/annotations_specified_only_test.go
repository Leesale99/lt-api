package main

// Tests for the specified-only half of policy 15 (annotations.go
// checkAnnotationsResolve): an annotation in non-test Go code that resolves to
// a rule with status `specified-only` is an ERROR — the rule has no code yet,
// so no site may claim it. Test files keep the old behavior: their annotations
// resolve (or not) exactly as before, but a specified-only target stays legal
// there because a test names its rule through `verification`, not through an
// annotation.
//
// The synthetic trees are built under the OS temp dir: they carry Go sources
// that must never be scanned by the repository's own checker.

import (
	"strings"
	"testing"
)

func specifiedOnlyRule(id string) Rule {
	return Rule{
		ID:        id,
		Title:     "A pending thing is pending",
		Statement: "A pending thing has no code yet.",
		Module:    "kernel",
		Entity:    "thing",
		Category:  "orchestration",
		Status:    statusSpecifiedOnly,
	}
}

func TestAnnotationsRejectSpecifiedOnlyInNonTestSource(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "internal/kernel/thing/pending.go",
		"package thing\n\n//rule:R-KERNEL-002\nfunc Pending() {}\n")

	catalog := append(baseCatalog(), specifiedOnlyRule("R-KERNEL-002"))
	findings := collectAnnotations(catalog, repo.root)

	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1 for the specified-only annotation:\n%s",
			len(findings), describeFindings(findings))
	}
	f := findings[0]
	if f.Policy != policyAnnotations || f.Severity != severityError {
		t.Fatalf("finding = %+v, want an error under %s", f, policyAnnotations)
	}
	for _, want := range []string{
		"internal/kernel/thing/pending.go:",
		"//rule:R-KERNEL-002",
		"specified-only",
	} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not mention %q", f.Message, want)
		}
	}
}

func TestAnnotationsAllowSpecifiedOnlyInTestFile(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "internal/kernel/thing/pending_test.go",
		"package thing\n\nimport \"testing\"\n\n//rule:R-KERNEL-002\nfunc TestPending(t *testing.T) {}\n")

	catalog := append(baseCatalog(), specifiedOnlyRule("R-KERNEL-002"))
	if findings := collectAnnotations(catalog, repo.root); len(findings) != 0 {
		t.Fatalf("a test file's annotation to a specified-only rule was rejected:\n%s",
			describeFindings(findings))
	}
}

func TestAnnotationsAllowImplementedRuleInNonTestSource(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "internal/kernel/thing/extra.go",
		"package thing\n\n//rule:R-KERNEL-001\nfunc Extra() {}\n")

	if findings := collectAnnotations(baseCatalog(), repo.root); len(findings) != 0 {
		t.Fatalf("an annotation to an implemented rule was rejected:\n%s",
			describeFindings(findings))
	}
}
