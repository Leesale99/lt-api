package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalog is the top-level shape of docs/spec/rules.yaml: a flat sequence of
// rules. Field shapes follow docs/spec/rules.schema.md §2.
type Catalog []Rule

// Rule is one business rule. Nullable fields are pointers so that "absent" and
// "explicitly null" stay distinguishable where the policies care.
type Rule struct {
	ID           string        `yaml:"id"`
	Title        string        `yaml:"title"`
	Statement    string        `yaml:"statement"`
	Module       string        `yaml:"module"`
	Entity       string        `yaml:"entity"`
	Category     string        `yaml:"category"`
	Status       string        `yaml:"status"`
	Authority    string        `yaml:"authority"`
	Mechanism    *string       `yaml:"mechanism"`
	Scope        *string       `yaml:"scope"`
	Enforcement  []Enforcement `yaml:"enforcement"`
	Refusal      *Refusal      `yaml:"refusal"`
	Fixture      *string       `yaml:"fixture"`
	Verification Verification  `yaml:"verification"`
	ExpertRef    *string       `yaml:"expert-ref"`
	// KnowledgeRef points at the knowledge-base note behind the rule. It is
	// additional to expert-ref (never a replacement): policy 16 requires the
	// path to exist and to sit under docs/league-tokens-learning-phases/.
	KnowledgeRef *string `yaml:"knowledge-ref"`
	Notes        string  `yaml:"notes"`
}

// Enforcement is one place a rule is enforced. Site is `path::Symbol` for Go or
// a `migrations/...` id for SQL; never a line number.
type Enforcement struct {
	Site     string  `yaml:"site"`
	Layer    string  `yaml:"layer"`
	Role     string  `yaml:"role"`
	PinnedBy *string `yaml:"pinned_by"`
}

// Refusal is the SQLSTATE → constraint → sentinel → HTTP chain, when the rule
// can refuse at all.
type Refusal struct {
	Code       string     `yaml:"code"`
	Constraint StringList `yaml:"constraint"`
	Sentinel   *string    `yaml:"sentinel"`
	HTTP       *int       `yaml:"http"`
}

// StringList accepts a YAML scalar, a sequence, or null. `refusal.constraint`
// is documented as string | list | null.
type StringList []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!null" {
		*s = nil
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		*s = StringList{node.Value}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

// Verification points at proofs that exist today: Go tests (unit, e2e) by
// `path::TestSymbol`, and executable scenarios by
// `<feature-file-path>::<scenario name>` (cross-validated by policy 10). Any one
// of the three satisfies policy 3; unit and e2e are additionally resolved
// symbol-by-symbol.
type Verification struct {
	Unit    []string `yaml:"unit"`
	E2E     []string `yaml:"e2e"`
	Gherkin []string `yaml:"gherkin"`
}

// loadCatalog reads and decodes the canonical catalog. Decoding is strict in two
// ways. First, an unknown field is an error, because a misspelled key
// (`knowledge_ref:` for `knowledge-ref:`) would otherwise be dropped silently, and
// the policies that treat an absent key as legal (policy 16) could never notice
// the typo. Second, the catalog is exactly one YAML document: a Decoder yields
// one document per Decode call, so decoding once would drop everything after a
// `---` separator (a merge accident) and still return a parsable catalog.
func loadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var catalog Catalog
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	// Keep decoding until EOF: a second document, or malformed content after the
	// first one, means the file is not the one-document catalog we expect.
	trailing := fmt.Errorf("parse catalog %s: trailing YAML document after the first — "+
		"the catalog must be exactly one document", path)
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		// Exactly one document: the catalog.
	case err != nil:
		return nil, fmt.Errorf("%w: %w", trailing, err)
	default:
		return nil, trailing
	}
	return catalog, nil
}

// repoRoot resolves the tree that catalog paths are relative to. The catalog
// lives at <root>/docs/spec/rules.yaml, so its directory's parent is the root.
// Any other location falls back to the working directory (tests exercise the
// docs/spec layout).
func repoRoot(rulesPath string) (string, error) {
	abs, err := filepath.Abs(rulesPath)
	if err != nil {
		return "", fmt.Errorf("resolve catalog path: %w", err)
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == "spec" && filepath.Base(filepath.Dir(dir)) == "docs" {
		return filepath.Dir(filepath.Dir(dir)), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return wd, nil
}

// splitPointer splits a `path::Symbol` pointer (extra `::Subtest` parts are
// allowed on verification pointers and ignored for symbol lookup).
func splitPointer(pointer string) (path, symbol string, ok bool) {
	parts := strings.Split(pointer, "::")
	if len(parts) < 2 {
		return "", "", false
	}
	path = strings.TrimSpace(parts[0])
	symbol = strings.TrimSpace(parts[1])
	if path == "" || symbol == "" {
		return "", "", false
	}
	return path, symbol, true
}

// displayPath keeps site/verification pointers readable in Markdown tables.
func displayPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// normalizeProse folds a YAML block scalar into stable single-spaced text so
// the generated Markdown does not depend on the YAML author's line wrapping.
func normalizeProse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sortedKeys returns map keys in ascending order for deterministic output.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
