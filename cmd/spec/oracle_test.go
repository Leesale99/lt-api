package main

// Tests for checker policy 18 (`18-oracle`): a Then step must assert an
// observable outcome, category-bound rules must carry the right kind of
// assertion, and an expected literal may not be copied from executable Go.

import (
	"path/filepath"
	"strings"
	"testing"
)

func oracleRepo(t *testing.T, feature string) *testRepo {
	t.Helper()
	repo := newTestRepo(t)
	repo.addFile(t, "docs/spec/features/kernel/oracle.feature",
		readTestdata(t, "oracle/"+feature))
	return repo
}

func oracleFindings(t *testing.T, repo *testRepo, catalog Catalog) []Finding {
	t.Helper()
	findings := collectOracle(catalog, repo.root)
	if len(findings) != 0 {
		for _, f := range findings {
			if f.Policy != policyOracle {
				t.Errorf("oracle collector returned a non-oracle finding: %s", describeFindings(findings))
				break
			}
		}
	}
	return findings
}

func TestOracleGreenScenario(t *testing.T) {
	repo := oracleRepo(t, "green.feature")
	rule := baseRule()
	rule.Category = "invariant"

	if findings := oracleFindings(t, repo, Catalog{rule}); len(findings) != 0 {
		t.Fatalf("observable scenarios produced policy 18 findings:\n%s", describeFindings(findings))
	}
}

func TestOracleRejectsVacuousThen(t *testing.T) {
	repo := oracleRepo(t, "vacuous.feature")
	findings := oracleFindings(t, repo, Catalog{baseRule()})

	group := findingsFor(findings, policyOracle)
	if len(group) != 1 {
		t.Fatalf("%d policy 18 findings, want 1:\n%s", len(group), describeFindings(findings))
	}
	for _, want := range []string{"vacuous Then step", "the call succeeds", "assert a concrete state"} {
		if !strings.Contains(group[0].Message, want) {
			t.Errorf("finding does not mention %q: %s", want, group[0].Message)
		}
	}
	if group[0].Subject != "R-KERNEL-001" {
		t.Errorf("finding names rule %q, want R-KERNEL-001", group[0].Subject)
	}
}

func TestOracleRequiresAnInvariantOutcome(t *testing.T) {
	repo := oracleRepo(t, "no-outcome.feature")
	rule := baseRule()
	rule.Category = "invariant"

	group := findingsFor(oracleFindings(t, repo, Catalog{rule}), policyOracle)
	if len(group) != 1 {
		t.Fatalf("%d policy 18 findings, want 1:\n%s", len(group), describeFindings(group))
	}
	if !strings.Contains(group[0].Message, "binds state or side effect") {
		t.Errorf("finding does not name the state binding: %s", group[0].Message)
	}
}

func TestOracleRequiresAnInterfaceRefusal(t *testing.T) {
	repo := oracleRepo(t, "refusal-missing.feature")
	rule := baseRule()
	rule.Category = "interface"
	rule.Refusal = &Refusal{Code: "synthetic", HTTP: ptr(403)}

	group := findingsFor(oracleFindings(t, repo, Catalog{rule}), policyOracle)
	if len(group) != 1 {
		t.Fatalf("%d policy 18 findings, want 1:\n%s", len(group), describeFindings(group))
	}
	if !strings.Contains(group[0].Message, "binds refusal") {
		t.Errorf("finding does not name the refusal binding: %s", group[0].Message)
	}
}

func TestOracleRejectsReDerivedLiteral(t *testing.T) {
	repo := oracleRepo(t, "rederived.feature")
	repo.addFile(t, "internal/kernel/thing/thing.go",
		"package thing\n\n//rule:R-KERNEL-001\nfunc Do() { panic(\"boom\") }\n")

	group := findingsFor(oracleFindings(t, repo, Catalog{baseRule()}), policyOracle)
	if len(group) != 1 {
		t.Fatalf("%d policy 18 findings, want 1:\n%s", len(group), describeFindings(group))
	}
	for _, want := range []string{"already appears verbatim", `"boom"`} {
		if !strings.Contains(group[0].Message, want) {
			t.Errorf("finding does not mention %q: %s", want, group[0].Message)
		}
	}
}

func TestOracleExemptsDeclarationVocabulary(t *testing.T) {
	repo := oracleRepo(t, "green.feature")
	repo.addFile(t, "internal/kernel/thing/thing.go",
		"package thing\n\n//rule:R-KERNEL-001\nconst locked = \"locked\"\n\nfunc Do() { _ = locked }\n")

	rule := baseRule()
	rule.Category = "invariant"
	if findings := oracleFindings(t, repo, Catalog{rule}); len(findings) != 0 {
		t.Fatalf("a declaration literal was treated as an expectation:\n%s", describeFindings(findings))
	}
}

func TestOracleRealTreePasses(t *testing.T) {
	root := filepath.Join("..", "..")
	catalog, err := loadCatalog(filepath.Join(root, "docs", "spec", "rules"))
	if err != nil {
		t.Fatalf("load the real catalog: %v", err)
	}
	if findings := collectOracle(catalog, root); len(findings) != 0 {
		t.Fatalf("the real feature tree violates policy 18:\n%s", describeFindings(findings))
	}
}
