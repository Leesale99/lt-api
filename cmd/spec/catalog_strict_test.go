package main

// Tests for strict catalog decoding (catalog.go loadCatalog). The catalog is
// hand-edited YAML, so a misspelled key (`knowledge_ref:` for `knowledge-ref:`)
// used to be dropped silently — and policy 16's "absent is legal" semantics mean
// nothing else would catch the typo. loadCatalog must reject unknown fields.
//
// Fixtures live under testdata/catalog (the same directory as the policy
// fixtures) because the checker's own scan skips testdata/.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadCatalogRejectsUnknownField is the typo case: a rule-level key the
// Catalog/Rule structs do not declare.
func TestLoadCatalogRejectsUnknownField(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("testdata", "catalog", "unknown-field-typo.yaml"))
	if err == nil {
		t.Fatalf("loadCatalog accepted a catalog with a misspelled key (decoded %d rules); "+
			"a dropped key is undetectable downstream", len(catalog))
	}
	for _, want := range []string{
		"unknown-field-typo.yaml",
		"knowledge_ref",
		"not found in type",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestLoadCatalogRejectsUnknownFieldInRefusal pins that strictness survives the
// custom UnmarshalYAML on StringList: the typo is a sibling of `constraint`, so
// the enclosing struct decoder must still see it.
func TestLoadCatalogRejectsUnknownFieldInRefusal(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("testdata", "catalog", "unknown-field-in-refusal.yaml"))
	if err == nil {
		t.Fatalf("loadCatalog accepted a refusal with an unknown key (decoded %d rules)", len(catalog))
	}
	for _, want := range []string{
		"unknown-field-in-refusal.yaml",
		"https",
		"not found in type",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestLoadCatalogDecodesRealCatalog is the completeness guard: every key the
// canonical catalog uses must exist on the structs, so strict decoding stays
// green against the tree it is meant to validate. It also pins that StringList
// still accepts both documented shapes (scalar and list `constraint`).
func TestLoadCatalogDecodesRealCatalog(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "docs", "spec", "rules.yaml"))
	if err != nil {
		t.Fatalf("strict decoding rejected the canonical catalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("the canonical catalog decoded to zero rules")
	}
	for _, rule := range catalog {
		if rule.ID == "" {
			t.Fatalf("a rule decoded without an id: %+v", rule)
		}
	}
}
