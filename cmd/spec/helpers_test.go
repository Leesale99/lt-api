package main

import (
	"os"
	"path/filepath"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// testRepo is a synthetic checkout: a per-rule-file catalog plus the Go files its
// sites and verification pointers name. Tests never read the real catalog.
type testRepo struct {
	root      string
	rulesPath string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	root := t.TempDir()
	repo := &testRepo{
		root:      root,
		rulesPath: filepath.Join(root, "docs", "spec", "rules"),
	}
	repo.addFile(t, "internal/kernel/thing/thing.go", "package thing\n\n//rule:R-KERNEL-001\nfunc Do() {}\n")
	repo.addFile(t, "internal/kernel/thing/other.go", "package thing\n\nfunc Other() {}\n\ntype Token struct{}\n")
	repo.addFile(t, "internal/kernel/thing/thing_test.go",
		"package thing\n\nimport \"testing\"\n\nfunc TestDo(t *testing.T) {}\n")
	repo.addFile(t, "docs/league-tokens-learning-phases/phase-03.md", "# Transactions\n")
	// A valid (if empty) step-vocabulary allowlist: policy 13 needs one, and
	// every module owns a section even where no feature file exists.
	repo.addFile(t, "docs/spec/glossary.yaml",
		"common: []\ngame: []\nidentity: []\nschedule: []\nledger: []\nkernel: []\nplatform: []\n")
	return repo
}

// removeFile deletes a file the baseline repo seeded, so a test can exercise a
// checkout that is missing it.
func (r *testRepo) removeFile(t *testing.T, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

func (r *testRepo) addFile(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(r.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// writeCatalog writes the rules as a per-rule-file catalog: one file per rule at
// docs/spec/rules/<module>/<ID>.md, rendered by the canonical writer
// (catalog.go renderRuleFile — the writer rulefile_test.go round-trips over the
// live catalog). Fixtures are therefore
// minimal-valid front matter plus a `## Notes` body when the rule carries one,
// and a test that mutates a Rule writes the new format, never legacy YAML.
func (r *testRepo) writeCatalog(t *testing.T, catalog Catalog) {
	t.Helper()
	dir := filepath.Join(r.root, "docs", "spec", "rules")
	// Replace the tree so a second writeCatalog call cannot leave a stale rule
	// file behind (the loader would decode both).
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("reset catalog dir: %v", err)
	}
	// An empty catalog is a legal state (the queue renders it); the directory
	// exists and holds no rule file.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create catalog dir: %v", err)
	}
	for _, rule := range catalog {
		file, err := renderRuleFile(rule)
		if err != nil {
			t.Fatalf("render %s: %v", rule.ID, err)
		}
		r.addFile(t, filepath.ToSlash(filepath.Join("docs", "spec", "rules", rule.Module, rule.ID+".md")), string(file))
	}
}

// baseRule is a minimal catalog entry that satisfies every policy: one
// authoritative site (annotated, policy 15), one unit pointer, authority ==
// category default, no refusal, no notes. It picks `orchestration`, the
// category policies 14 leaves unconstrained, so a test only sees category
// findings when it sets the category it exercises.
func baseRule() Rule {
	return Rule{
		ID:        "R-KERNEL-001",
		Title:     "A thing always is a thing",
		Statement: "A thing always is. It is never almost.",
		Module:    "kernel",
		Entity:    "thing",
		Category:  "orchestration",
		Status:    statusImplemented,
		Authority: "service",
		Enforcement: []Enforcement{{
			Site:  "internal/kernel/thing/thing.go::Do",
			Layer: "domain",
			Role:  "authoritative",
		}},
		Verification: Verification{
			Unit: []string{"internal/kernel/thing/thing_test.go::TestDo"},
		},
	}
}

func baseCatalog() Catalog {
	return Catalog{baseRule()}
}
