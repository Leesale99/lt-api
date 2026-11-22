package main

// Tests for the per-rule-file catalog loader (catalog.go loadCatalog). The
// catalog is hand-edited Markdown, so two classes of mistake must be caught at
// load time rather than downstream: a misspelled front-matter key (policy 16's
// "absent is legal" semantics mean nothing else would notice `knowledge_ref:`
// for `knowledge-ref:`), and a file whose path and entry disagree (a rule in the
// wrong module directory, a file named after another id, two files claiming one
// id). The fixtures are written per test from renderRuleFile output, so they are
// always the live format.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRulesTree writes a rules directory from relative path → content and
// returns the directory. Every entry under it is the caller's: the helper adds
// no implicit fixtures.
func writeRulesTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create rules dir: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// renderedRule is one fixture rule file as the live writer emits it.
func renderedRule(t *testing.T, rule Rule) string {
	t.Helper()
	file, err := renderRuleFile(rule)
	if err != nil {
		t.Fatalf("render %s: %v", rule.ID, err)
	}
	return string(file)
}

// TestLoadCatalogRejectsUnknownField is the typo case: a rule-level key the Rule
// struct does not declare.
func TestLoadCatalogRejectsUnknownField(t *testing.T) {
	file := strings.Replace(renderedRule(t, baseRule()), "id: R-KERNEL-001\n",
		"id: R-KERNEL-001\nknowledge_ref: docs/league-tokens-learning-phases/phase-03.md\n", 1)
	dir := writeRulesTree(t, map[string]string{"kernel/R-KERNEL-001.md": file})

	catalog, err := loadCatalog(dir)
	if err == nil {
		t.Fatalf("loadCatalog accepted a rule file with a misspelled key (decoded %d rules); "+
			"a dropped key is undetectable downstream", len(catalog))
	}
	for _, want := range []string{"kernel/R-KERNEL-001.md", "knowledge_ref", "not found in type"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestLoadCatalogRejectsUnknownFieldInRefusal pins that strictness survives the
// custom UnmarshalYAML on StringList: the typo is a sibling of `constraint`, so
// the enclosing struct decoder must still see it.
func TestLoadCatalogRejectsUnknownFieldInRefusal(t *testing.T) {
	rule := baseRule()
	rule.Refusal = &Refusal{Code: "P0001", Constraint: StringList{"things_state_gate"}}
	file := strings.Replace(renderedRule(t, rule), "  http: null\n", "  https: 409\n", 1)
	if !strings.Contains(file, "https: 409") {
		t.Fatalf("the rendered fixture carries no refusal.http to misspell:\n%s", file)
	}
	dir := writeRulesTree(t, map[string]string{"kernel/R-KERNEL-001.md": file})

	catalog, err := loadCatalog(dir)
	if err == nil {
		t.Fatalf("loadCatalog accepted a refusal with an unknown key (decoded %d rules)", len(catalog))
	}
	for _, want := range []string{"kernel/R-KERNEL-001.md", "https", "not found in type"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestLoadCatalogRejectsMisfiledRule pins that the path and the entry can never
// disagree: the id is the file stem and the module is the parent directory, so a
// misplaced rule is a load error rather than a rule the loader silently accepts
// in the wrong place (or a file whose name says nothing about its content).
func TestLoadCatalogRejectsMisfiledRule(t *testing.T) {
	valid := renderedRule(t, baseRule())

	otherModule := baseRule()
	otherModule.Module = "game"
	renamedID := baseRule()
	renamedID.ID = "R-KERNEL-002"

	tests := []struct {
		name  string
		files map[string]string
		wants []string
	}{
		{
			name:  "id does not match the file name",
			files: map[string]string{"kernel/R-KERNEL-001.md": renderedRule(t, renamedID)},
			wants: []string{"R-KERNEL-001.md", "does not match the file name", "R-KERNEL-002"},
		},
		{
			name:  "module does not match the directory",
			files: map[string]string{"kernel/R-KERNEL-001.md": renderedRule(t, otherModule)},
			wants: []string{"kernel/R-KERNEL-001.md", "does not match the directory", "game"},
		},
		{
			name:  "module directory is not in the enum",
			files: map[string]string{"bogus/R-KERNEL-001.md": valid},
			wants: []string{"bogus", "not in the frozen module enum"},
		},
		{
			name: "duplicate id across modules",
			files: map[string]string{
				"kernel/R-KERNEL-001.md": valid,
				"game/R-KERNEL-001.md":   renderedRule(t, func() Rule { r := baseRule(); r.Module = "game"; return r }()),
			},
			wants: []string{"R-KERNEL-001", "is defined in both"},
		},
		{
			name:  "non-markdown file",
			files: map[string]string{"kernel/notes.txt": "not a rule\n"},
			wants: []string{"notes.txt", "not a Markdown rule file"},
		},
		{
			name:  "dotfile",
			files: map[string]string{"kernel/.DS_Store": "junk\n"},
			wants: []string{".DS_Store", "dotfile"},
		},
		{
			name:  "dotfile at the top level",
			files: map[string]string{".hidden": "junk\n"},
			wants: []string{".hidden", "dotfile"},
		},
		{
			name:  "nested directory",
			files: map[string]string{"kernel/nested/R-KERNEL-002.md": valid},
			wants: []string{"nested", "one rule per file"},
		},
		{
			name:  "stray top-level file",
			files: map[string]string{"R-KERNEL-001.md": valid},
			wants: []string{"R-KERNEL-001.md", "is not a module directory"},
		},
		{
			name:  "stray yaml beside the rules",
			files: map[string]string{"kernel/R-KERNEL-001.md": valid, "rules.yaml": "[]\n"},
			wants: []string{"rules.yaml", "is not a module directory"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog, err := loadCatalog(writeRulesTree(t, tt.files))
			if err == nil {
				t.Fatalf("loadCatalog accepted a misfiled catalog (decoded %d rules)", len(catalog))
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err.Error(), want)
				}
			}
		})
	}
}

// TestLoadCatalogAcceptsReadmeAndOrders pins the one authored non-rule file the
// directory carries (README.md) and the deterministic catalog order: module enum
// order first, then id ascending, independent of readdir order.
func TestLoadCatalogAcceptsReadmeAndOrders(t *testing.T) {
	first := baseRule()
	first.ID, first.Module = "R-KERNEL-002", "kernel"
	second := baseRule()
	second.ID, second.Module = "R-GAME-001", "game"
	dir := writeRulesTree(t, map[string]string{
		"README.md":              "# the rules directory\n",
		"kernel/R-KERNEL-002.md": renderedRule(t, first),
		"game/R-GAME-001.md":     renderedRule(t, second),
	})

	catalog, err := loadCatalog(dir)
	if err != nil {
		t.Fatalf("loadCatalog rejected a rules directory with a README: %v", err)
	}
	var got []string
	for _, rule := range catalog {
		got = append(got, rule.ID)
	}
	want := []string{"R-GAME-001", "R-KERNEL-002"} // game before kernel in the enum
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("catalog order = %v, want module enum order then id %v", got, want)
	}
}

// TestLoadCatalogAcceptsEmptyCatalog: the loader keeps the retired catalog's
// behavior for an empty tree (the queue renders "no rule in scope" rather than
// failing), while a missing directory is still an error.
func TestLoadCatalogAcceptsEmptyCatalog(t *testing.T) {
	dir := writeRulesTree(t, nil)
	catalog, err := loadCatalog(dir)
	if err != nil {
		t.Fatalf("loadCatalog rejected an empty rules directory: %v", err)
	}
	if len(catalog) != 0 {
		t.Fatalf("an empty rules directory decoded %d rules", len(catalog))
	}
	if _, err := loadCatalog(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("loadCatalog accepted a missing rules directory")
	}
}

// TestLoadCatalogDecodesRealCatalog is the completeness guard: every key the
// canonical catalog uses must exist on the structs, so strict decoding stays
// green against the tree it is meant to validate, and the path/identity rules
// hold for all 126 files. It also pins that StringList still accepts both
// documented shapes (scalar and list `constraint`).
func TestLoadCatalogDecodesRealCatalog(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "docs", "spec", "rules"))
	if err != nil {
		t.Fatalf("strict decoding rejected the canonical catalog: %v", err)
	}
	if len(catalog) != 126 {
		t.Fatalf("the canonical catalog decoded to %d rules, want 126", len(catalog))
	}
	seen := map[string]bool{}
	for _, rule := range catalog {
		if rule.ID == "" {
			t.Fatalf("a rule decoded without an id: %+v", rule)
		}
		if seen[rule.ID] {
			t.Fatalf("the canonical catalog carries %s twice", rule.ID)
		}
		seen[rule.ID] = true
	}
}
