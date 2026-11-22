package main

// Tests for `spec check --module` / `--rule`: every policy still runs, and the
// scoped report narrows by SUBJECT rather than by hiding whole policies:
//
//   - a rule-attributed finding (a non-empty subject) is reported only when its
//     rule is in scope, and it still fails --strict;
//   - a catalog-level finding (an empty subject) is ALWAYS reported, downgraded
//     to WARN while scoped, so global rot stays visible without failing a
//     worker's --strict gate on someone else's problem;
//   - a feature-level policy's finding is never narrowed (the feature tree is one
//     artifact, and a half-printed cross-validation would read as if the rest of
//     the tree were verified).
//
// The scoped gate must be able to FAIL on the mistakes a scoped worker makes:
// a step-word violation or an annotation typo inside the rules it owns used to
// be invisible because those findings carried a pointer or a label instead of a
// rule id (B2.2).

import (
	"bytes"
	"strings"
	"testing"
)

func runCheckArgsForTest(t *testing.T, repo *testRepo, args ...string) (int, string) {
	t.Helper()
	args = append([]string{"--rules", repo.rulesPath}, args...)
	var stdout, stderr bytes.Buffer
	code := runCheck(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func gameRule() Rule {
	rule := baseRule()
	rule.ID = "R-GAME-001"
	rule.Module = "game"
	rule.Title = "A game thing always is a thing"
	rule.Statement = "A game thing always is."
	return rule
}

// scopeGlossary declares the words a scoped fixture's good steps say, so a
// vocabulary finding is the test's own mutation and never a missing seed.
const scopeGlossary = `common:
  - a
  - the
game:
  - answer
  - carries
identity: []
schedule: []
ledger: []
kernel:
  - answer
  - carries
  - thing
platform: []
`

func TestCheckScopeFiltersFindings(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "internal/game/thing/thing.go", "package thing\n\n//rule:R-GAME-001\nfunc Do() {}\n")
	repo.addFile(t, "internal/game/thing/thing_test.go",
		"package thing\n\nimport \"testing\"\n\nfunc TestDo(t *testing.T) {}\n")
	brokenKernel := baseRule()
	brokenKernel.Verification = Verification{} // policy 3 error in kernel
	game := gameRule()
	game.Enforcement[0].Site = "internal/game/thing/thing.go::Do"
	game.Verification.Unit = []string{"internal/game/thing/thing_test.go::TestDo"}
	repo.writeCatalog(t, Catalog{brokenKernel, game})

	if code, _ := runCheckArgsForTest(t, repo, "--strict"); code != 1 {
		t.Fatalf("unscoped strict check = %d, want 1", code)
	}

	code, output := runCheckArgsForTest(t, repo, "--module", "game", "--strict")
	if code != 0 {
		t.Fatalf("scoped game strict check = %d, want 0:\n%s", code, output)
	}
	if strings.Contains(output, "R-KERNEL-001") {
		t.Errorf("scoped output leaks the out-of-scope kernel finding:\n%s", output)
	}
	if !strings.Contains(output, "scope: module game") {
		t.Errorf("header does not name the active scope:\n%s", output)
	}

	if code, _ := runCheckArgsForTest(t, repo, "--rule", "R-GAME-001", "--strict"); code != 0 {
		t.Errorf("scoped clean rule strict check = %d, want 0", code)
	}
	if code, _ := runCheckArgsForTest(t, repo, "--module", "kernel", "--strict"); code != 1 {
		t.Errorf("scoped broken module strict check = %d, want 1", code)
	}
	if code, _ := runCheckArgsForTest(t, repo, "--rule", "R-KERNEL-001", "--strict"); code != 1 {
		t.Errorf("scoped broken rule strict check = %d, want 1", code)
	}
}

// TestCheckScopeReportsCatalogLevelFindingsAsWarnings is the flip of the old
// TestCheckScopeHidesCatalogLevelFindings. A catalog-level finding names no rule,
// so no scope can implicate it: hiding it made the scoped report silently
// incomplete. It is reported, downgraded to WARN, so a scoped --strict run still
// exits 0 — the worker is told about the rot, but is not blocked by it.
func TestCheckScopeReportsCatalogLevelFindingsAsWarnings(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())
	repo.addFile(t, "docs/spec/features/kernel/orphan.feature",
		"Feature: an orphan\n\n  Scenario: no rule names me\n    Then the thing reads \"locked\"\n")

	code, output := runCheckArgsForTest(t, repo, "--strict")
	if code != 1 {
		t.Fatalf("unscoped strict check = %d, want 1 (the untagged scenario is an error):\n%s", code, output)
	}
	if line := findingLine(output, "carries no @rule tag"); strings.HasPrefix(line, "- WARN ") {
		t.Errorf("unscoped output downgrades the catalog-level error to WARN:\n%s", output)
	}

	code, output = runCheckArgsForTest(t, repo, "--module", "kernel", "--strict")
	if code != 0 {
		t.Fatalf("scoped strict check = %d, want 0 (catalog-level findings warn, never fail):\n%s", code, output)
	}
	line := findingLine(output, "carries no @rule tag")
	if line == "" {
		t.Fatalf("scoped output hides the catalog-level finding:\n%s", output)
	}
	if !strings.HasPrefix(line, "- WARN ") {
		t.Errorf("the scoped catalog-level finding is not downgraded to WARN:\n%s", line)
	}
}

// TestCheckScopeFailsOnScopedVocabularyViolation is the first half of the B2.2
// defect fix: a denylisted transport word in a scenario tagged with an
// in-scope rule fails the scoped --strict gate. Before the fix the finding's
// subject was the `file::scenario` pointer, which no scope matches.
func TestCheckScopeFailsOnScopedVocabularyViolation(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())
	repo.addFile(t, "docs/spec/glossary.yaml", scopeGlossary)
	repo.addFile(t, "docs/spec/features/kernel/thing.feature",
		"@kernel\nFeature: a thing's steps\n\n  @rule:R-KERNEL-001\n  Scenario: the wire's word leaks into a step\n    Then the answer carries a JSON payload\n")

	code, output := runCheckArgsForTest(t, repo, "--module", "kernel", "--strict")
	if code != 1 {
		t.Fatalf("scoped kernel strict check = %d, want 1 (the transport words are errors in scope):\n%s", code, output)
	}
	for _, want := range []string{
		`step uses "json"`,
		`step uses "payload"`,
		"**R-KERNEL-001**",
		"docs/spec/features/kernel/thing.feature::the wire's word leaks into a step",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("scoped output does not report %q:\n%s", want, output)
		}
	}
}

// TestCheckScopeNarrowsVocabularyToTheScopedRule is the other half: the same
// violation in ANOTHER rule's scenario stays out of a scoped report, so the fix
// attributes findings to rules instead of making every vocabulary error global.
func TestCheckScopeNarrowsVocabularyToTheScopedRule(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, Catalog{baseRule(), gameRule()})
	repo.addFile(t, "docs/spec/glossary.yaml", scopeGlossary)
	repo.addFile(t, "docs/spec/features/game/thing.feature",
		"@game\nFeature: a game thing's steps\n\n  @rule:R-GAME-001\n  Scenario: the wire's word leaks into a step\n    Then the answer carries a JSON payload\n")

	if code, output := runCheckArgsForTest(t, repo, "--module", "game", "--strict"); code != 1 {
		t.Fatalf("scoped game strict check = %d, want 1:\n%s", code, output)
	}
	code, output := runCheckArgsForTest(t, repo, "--module", "kernel", "--strict")
	if code != 0 {
		t.Fatalf("scoped kernel strict check = %d, want 0 (the violation belongs to R-GAME-001):\n%s", code, output)
	}
	if strings.Contains(output, `step uses "json"`) {
		t.Errorf("scoped kernel output leaks the out-of-scope vocabulary finding:\n%s", output)
	}
}

// TestCheckScopeFailsOnMalformedAnnotationBesideScopedRule pins the annotation
// half: a malformed extra `//rule:` in the SAME comment group as the scoped
// rule's real annotation is attributed to that rule, so the scoped gate fails.
func TestCheckScopeFailsOnMalformedAnnotationBesideScopedRule(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())
	repo.addFile(t, "internal/kernel/thing/extra.go",
		"package thing\n\n//rule:R-KERNEL-001\n//rule:R-KERNEL-1\nfunc Extra() {}\n")

	code, output := runCheckArgsForTest(t, repo, "--rule", "R-KERNEL-001", "--strict")
	if code != 1 {
		t.Fatalf("scoped strict check = %d, want 1 (the typo sits under the scoped rule):\n%s", code, output)
	}
	for _, want := range []string{"**R-KERNEL-001**", "which is not an id", "(malformed id)"} {
		if !strings.Contains(output, want) {
			t.Errorf("scoped output does not report %q:\n%s", want, output)
		}
	}
}

// TestCheckScopeMutationGuarantees is the mutation-style pair the stage asks for:
// each case starts from a fixture the scoped run passes, mutates ONE thing inside
// the scoped rule's surface, and requires the scoped run to ERROR. A gate that
// stays green here is blind where the worker works.
func TestCheckScopeMutationGuarantees(t *testing.T) {
	cleanFeature := "@kernel\nFeature: a thing's steps\n\n  @rule:R-KERNEL-001\n  Scenario: a declared step word\n    Then the answer carries a thing\n"

	t.Run("undeclared step word in a scoped scenario", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.writeCatalog(t, baseCatalog())
		repo.addFile(t, "docs/spec/glossary.yaml", scopeGlossary)
		repo.addFile(t, "docs/spec/features/kernel/thing.feature", cleanFeature)
		if code, output := runCheckArgsForTest(t, repo, "--module", "kernel", "--strict"); code != 0 {
			t.Fatalf("baseline scoped strict check = %d, want 0:\n%s", code, output)
		}

		// The mutation: one step word the glossary does not declare.
		repo.addFile(t, "docs/spec/features/kernel/thing.feature",
			strings.Replace(cleanFeature, "carries a thing", "carries a ponderous thing", 1))
		code, output := runCheckArgsForTest(t, repo, "--module", "kernel", "--strict")
		if code != 1 {
			t.Fatalf("scoped strict check = %d, want 1 after the mutation:\n%s", code, output)
		}
		if !strings.Contains(output, `step token "ponderous" is not declared for module "kernel"`) {
			t.Errorf("scoped output does not report the undeclared word:\n%s", output)
		}
	})

	t.Run("unknown-id annotation in the scoped rule's comment group", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.writeCatalog(t, baseCatalog())
		repo.addFile(t, "internal/kernel/thing/extra.go",
			"package thing\n\n//rule:R-KERNEL-001\nfunc Extra() {}\n")
		if code, output := runCheckArgsForTest(t, repo, "--rule", "R-KERNEL-001", "--strict"); code != 0 {
			t.Fatalf("baseline scoped strict check = %d, want 0:\n%s", code, output)
		}

		// The mutation: a well-formed id the catalog never issued, stacked
		// under the scoped rule's own annotation.
		repo.addFile(t, "internal/kernel/thing/extra.go",
			"package thing\n\n//rule:R-KERNEL-001\n//rule:R-KERNEL-999\nfunc Extra() {}\n")
		code, output := runCheckArgsForTest(t, repo, "--rule", "R-KERNEL-001", "--strict")
		if code != 1 {
			t.Fatalf("scoped strict check = %d, want 1 after the mutation:\n%s", code, output)
		}
		for _, want := range []string{"**R-KERNEL-001**", "which names no rule in the catalog", "(unknown id R-KERNEL-999)"} {
			if !strings.Contains(output, want) {
				t.Errorf("scoped output does not report %q:\n%s", want, output)
			}
		}
	})
}

func TestCheckScopeFlagErrors(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())

	if code, output := runCheckArgsForTest(t, repo, "--module", "nope"); code != 2 {
		t.Errorf("invalid module exit = %d, want 2:\n%s", code, output)
	} else if !strings.Contains(output, "game, identity, kernel, ledger, platform, schedule") {
		t.Errorf("invalid module error does not list the enum:\n%s", output)
	}
	if code, _ := runCheckArgsForTest(t, repo, "--rule", "R-NOPE-999"); code != 1 {
		t.Errorf("unknown rule exit = %d, want 1", code)
	}
	if code, _ := runCheckArgsForTest(t, repo, "--module", "game", "--rule", "R-KERNEL-001"); code != 2 {
		t.Errorf("--module with --rule exit = %d, want 2", code)
	}
}

// findingLine returns the report line carrying want, or "" when no line does.
func findingLine(output, want string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}
