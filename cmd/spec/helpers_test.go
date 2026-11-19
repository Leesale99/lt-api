package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func ptr[T any](v T) *T { return &v }

// testRepo is a synthetic checkout: a docs/spec catalog plus the Go files its
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
		rulesPath: filepath.Join(root, "docs", "spec", "rules.yaml"),
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

func (r *testRepo) writeCatalog(t *testing.T, catalog Catalog) {
	t.Helper()
	data, err := yaml.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	r.addFile(t, "docs/spec/rules.yaml", string(data))
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
