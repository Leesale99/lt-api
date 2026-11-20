package main

// Tests for `spec check --module` / `--rule`: every policy still runs, but only
// findings whose rule id is in scope are printed or fail --strict. Catalog-level
// findings (empty rule id) are reported only unscoped.

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

func TestCheckScopeHidesCatalogLevelFindings(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())
	repo.addFile(t, "docs/spec/features/kernel/orphan.feature",
		"Feature: an orphan\n\n  Scenario: no rule names me\n    Then the thing reads \"locked\"\n")

	code, output := runCheckArgsForTest(t, repo, "--strict")
	if code != 1 {
		t.Fatalf("unscoped strict check = %d, want 1 (the untagged scenario is an error)", code)
	}
	if !strings.Contains(output, "carries no @rule tag") {
		t.Errorf("unscoped output does not report the catalog-level finding:\n%s", output)
	}

	code, output = runCheckArgsForTest(t, repo, "--module", "kernel", "--strict")
	if code != 0 {
		t.Fatalf("scoped strict check = %d, want 0 (empty-rule findings are unscoped only):\n%s", code, output)
	}
	if strings.Contains(output, "carries no @rule tag") {
		t.Errorf("scoped output leaks the catalog-level finding:\n%s", output)
	}
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
