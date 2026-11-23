package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// catalog.go owns the live catalog: one Markdown file per rule at
// docs/spec/rules/<module>/<ID>.md, the ONE front-matter + body parser that
// reads such a file, the canonical writer that emits one (renderRuleFile, at the
// end of this file — its exact inverse), and the directory loader that
// assembles them.
//
// # The file format
//
// A rule file is a strict YAML front matter between two `---` fences at the top
// of the file, followed by an optional Markdown body of authored prose:
//
//	---
//	<the machine fields, exactly the fields of Rule>
//	---
//
//	## Notes
//
//	<the residual human prose for this rule>
//
// Front matter decodes strictly (yaml.v3 KnownFields(true)) into exactly one YAML
// document, so a misspelled key is an error, `scope:` (the retired name) is an
// error, and `notes:` is an error — notes are not a front-matter field any more.
// Body prose is not machine state; the only thing the loader reads out of it is
// the prose under the single `## Notes` heading, captured as the rule's Notes
// string so policies 5 (drifted ⇒ notes), 7 (a copy tied by a tie note) and 8
// (authority ≠ category default ⇒ notes) stay satisfiable.
//
// # Canonical prose form
//
// Notes, stale_pointer and tie are all stored as `normalizeProse` output: every
// whitespace run (including the folded YAML line wrapping authors used to control
// by hand) collapses to one space, and the value carries no leading/trailing
// whitespace. An author's line wrapping therefore can never change the decoded
// value.

const (
	// ruleFileFence delimits the front matter. It is matched at line start only,
	// so an indented `---` inside a block scalar can never close the block.
	ruleFileFence = "---"

	// ruleNotesHeading is the one body section the loader reads.
	ruleNotesHeading = "## Notes"

	// catalogReadmeFile is the one authored prose file the rules directory
	// carries: its README explains the layout and the conventions. It is not a
	// rule, so the loader skips it (and only it) at the top level.
	catalogReadmeFile = "README.md"
)

// Catalog is a loaded per-rule-file catalog: one Rule per rule file, in module
// enum order then id order (see loadCatalog).
type Catalog []Rule

// Rule is one business rule. Nullable fields are pointers so that "absent" and
// "explicitly null" stay distinguishable where the policies care.
//
// WriteScope is the former `scope:` field renamed (`write_scope:`) so it can
// never be confused with run scoping; its values are unchanged
// (row-local/cross-row/cross-table). StalePointer and Tie were prose notes with
// `stale-pointer:`/`Tie:` markers in the retired single-file catalog and are
// first-class fields now. Notes is not a front-matter field at all: it is the prose under the
// body's `## Notes` heading, so its yaml tag is "-" and a `notes:` key in the
// front matter is a strict-decoding error.
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
	WriteScope   *string       `yaml:"write_scope"`
	Enforcement  []Enforcement `yaml:"enforcement"`
	Refusal      *Refusal      `yaml:"refusal"`
	Fixture      *string       `yaml:"fixture"`
	Verification Verification  `yaml:"verification"`
	ExpertRef    *string       `yaml:"expert-ref"`
	// KnowledgeRef points at the knowledge-base note behind the rule. It is
	// additional to expert-ref (never a replacement): policy 16 requires the
	// path to exist and to sit under docs/league-tokens-learning-phases/.
	KnowledgeRef *string `yaml:"knowledge-ref"`
	// StalePointer is a rot note: prose naming pointers that no longer resolve.
	// Policy 5 allows it only on implemented rules.
	StalePointer *string `yaml:"stale_pointer"`
	// Tie is the sentence tying a data-carrying copy to its authority; policy 7
	// accepts it in place of a pinned_by fixture.
	Tie   *string `yaml:"tie"`
	Notes string  `yaml:"-"`
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

// loadCatalog reads the per-rule-file catalog: one Markdown file per rule at
// <dir>/<module>/<ID>.md.
//
// The directory layout is strict, because a misplaced file would silently drop a
// rule. At the top level every entry is a module directory named after a value of
// the frozen module enum; the single exception is catalogReadmeFile, the
// directory's authored README, which is skipped. Inside a module directory every
// entry is a rule file: a regular, non-dotfile whose name ends in `.md`, one rule
// per file. A dotfile or a non-Markdown file anywhere under the rules directory is
// an error, not something to ignore — "ignored" is how a rule disappears.
//
// Per file, the front matter decodes strictly and the rule's `id` must equal the
// file's stem and its `module` must equal the parent directory name, so the path
// and the entry can never disagree. Across files, ids are unique.
//
// Ordering is deterministic and does not depend on readdir order: module enum
// order first (the schema's declaration order), then id ascending.
func loadCatalog(dir string) (Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog directory: %w", err)
	}
	seen := make(map[string]string, len(entries))
	var catalog Catalog
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("load catalog %s: %q is a dotfile; the rules directory holds "+
				"one directory per module", dir, name)
		}
		if !entry.IsDir() {
			if name == catalogReadmeFile {
				continue
			}
			return nil, fmt.Errorf("load catalog %s: %q is not a module directory (the only "+
				"non-rule file allowed here is %s)", dir, name, catalogReadmeFile)
		}
		if !moduleEnum[name] {
			return nil, fmt.Errorf("load catalog %s: module directory %q is not in the frozen "+
				"module enum (%s)", dir, name, strings.Join(catalogModules(), ", "))
		}
		rules, err := loadModuleRules(filepath.Join(dir, name), name)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			if other, ok := seen[rule.ID]; ok {
				return nil, fmt.Errorf("load catalog %s: %s is defined in both %s/ and %s/; ids "+
					"are unique across the catalog", dir, rule.ID, other, name)
			}
			seen[rule.ID] = name
		}
		catalog = append(catalog, rules...)
	}
	sort.SliceStable(catalog, func(i, j int) bool {
		mi, mj := moduleIndex(catalog[i].Module), moduleIndex(catalog[j].Module)
		if mi != mj {
			return mi < mj
		}
		return catalog[i].ID < catalog[j].ID
	})
	return catalog, nil
}

// loadModuleRules reads every rule file of one module directory. The directory
// name is the module, so a rule whose front matter names another module is a
// path/entry disagreement and an error.
func loadModuleRules(dir, module string) (Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module directory: %w", err)
	}
	var rules Catalog
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("load catalog %s: %q is a dotfile; a module directory holds "+
				"rule files only", dir, name)
		}
		if entry.IsDir() {
			return nil, fmt.Errorf("load catalog %s: %q is a directory; the layout is "+
				"<module>/<ID>.md — one rule per file", dir, name)
		}
		if filepath.Ext(name) != ".md" {
			return nil, fmt.Errorf("load catalog %s: %q is not a Markdown rule file", dir, name)
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read rule file: %w", err)
		}
		rule, err := parseRuleFile(data, filepath.Join(module, name))
		if err != nil {
			return nil, fmt.Errorf("load catalog: %w", err)
		}
		stem := strings.TrimSuffix(name, ".md")
		if rule.ID != stem {
			return nil, fmt.Errorf("load catalog: %s: front-matter id %q does not match the "+
				"file name %q", displayPath(filepath.Join(module, name)), rule.ID, stem)
		}
		if rule.Module != module {
			return nil, fmt.Errorf("load catalog: %s: front-matter module %q does not match the "+
				"directory %q", displayPath(filepath.Join(module, name)), rule.Module, module)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// moduleIndex is the rule's position in the frozen module order, used for the
// catalog's deterministic sort. An unknown module sorts last (policy 2 reports
// it); loadCatalog rejects an unknown module directory before this runs.
func moduleIndex(module string) int {
	for i, name := range moduleOrder {
		if name == module {
			return i
		}
	}
	return len(moduleOrder)
}

// parseRuleFile parses one rule file: a strict, single-document YAML front matter
// between the fences, plus the Notes prose from the body. Strict decoding is the
// point — it is what makes `scope:` (the retired name) and `notes:` (a field that
// no longer exists) hard errors instead of silently dropped keys.
func parseRuleFile(data []byte, name string) (Rule, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != ruleFileFence {
		return Rule{}, fmt.Errorf("parse rule file %s: the file must start with a %q front-matter fence",
			name, ruleFileFence)
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == ruleFileFence {
			closing = i
			break
		}
	}
	if closing < 0 {
		return Rule{}, fmt.Errorf("parse rule file %s: the front matter has no closing %q fence",
			name, ruleFileFence)
	}

	front := strings.Join(lines[1:closing], "\n")
	var rule Rule
	decoder := yaml.NewDecoder(strings.NewReader(front))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rule); err != nil {
		return Rule{}, fmt.Errorf("parse rule file %s: front matter: %w", name, err)
	}
	// The front matter is exactly one YAML document: a second `---` document
	// would be dropped silently.
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return Rule{}, fmt.Errorf("parse rule file %s: front matter must be exactly one YAML "+
			"document: %w", name, err)
	default:
		return Rule{}, fmt.Errorf("parse rule file %s: front matter must be exactly one YAML "+
			"document — found a second one", name)
	}

	notes, err := notesFromBody(lines[closing+1:])
	if err != nil {
		return Rule{}, fmt.Errorf("parse rule file %s: %w", name, err)
	}
	rule.Notes = notes
	return rule, nil
}

// notesFromBody extracts the rule's Notes from the Markdown body: the prose under
// the single `## Notes` heading, up to the next level-2 heading, in canonical
// (normalized) form. Other level-2 sections may follow — they are authored prose
// that is not machine state — but a non-empty body with no `## Notes` section is
// an error, because that prose would otherwise be invisible to the loader.
func notesFromBody(lines []string) (string, error) {
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != ruleNotesHeading {
			continue
		}
		var body []string
		for _, rest := range lines[i+1:] {
			if strings.HasPrefix(rest, "## ") || strings.TrimRight(rest, " \t") == "##" {
				break
			}
			body = append(body, rest)
		}
		return normalizeProse(strings.Join(body, "\n")), nil
	}
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return "", fmt.Errorf("body prose must live under a %q heading", strings.TrimSpace(ruleNotesHeading))
		}
	}
	return "", nil
}

// repoRoot resolves the tree that catalog paths are relative to. The catalog
// lives at <root>/docs/spec/rules, so its parent's parent is the root. Any other
// location falls back to the working directory (tests exercise the docs/spec
// layout).
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

// # The canonical writer
//
// renderRuleFile is the live format's ONE writer and the exact inverse of
// parseRuleFile; it lives here because the file format is this file's subject and
// nothing else reads or writes a rule file's bytes. No subcommand calls it (the
// catalog is hand-authored, and the one-shot `spec migrate` command that used it
// to write the tree is gone): its consumers are the tests — every fixture rule
// file is written by it, and rulefile_test.go pins parse∘render fidelity over the
// canonical catalog so the two halves can never drift.

// renderRuleFile renders one rule file: the front-matter fences, the machine
// fields, and — only when there is residual prose — a `## Notes` body. The result
// always has exactly one trailing newline (render.go's finishMarkdown), and a rule
// with no residual prose is front matter only.
func renderRuleFile(rule Rule) ([]byte, error) {
	front, err := ruleFrontMatter(rule)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(ruleFileFence)
	b.WriteString("\n")
	b.Write(front)
	if !bytes.HasSuffix(front, []byte("\n")) {
		b.WriteString("\n")
	}
	b.WriteString(ruleFileFence)
	b.WriteString("\n")
	if rule.Notes != "" {
		b.WriteString("\n")
		b.WriteString(ruleNotesHeading)
		b.WriteString("\n\n")
		b.WriteString(rule.Notes)
		b.WriteString("\n")
	}
	return finishMarkdown(&b), nil
}

// ruleFrontMatter encodes the machine fields as the YAML document inside the
// fences. Field order is fixed and every field is always emitted (null when
// absent), so a rule file is greppable and its bytes never depend on which part
// of the catalog the rule came from. The renderer works on yaml.Node values so
// the emitter cannot reflow a value: prose fields are literal blocks, and null /
// `[]` stay distinguishable (a nil slice and an empty slice are different values
// to reflect.DeepEqual and therefore to the round-trip test).
func ruleFrontMatter(rule Rule) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key string, value *yaml.Node) {
		root.Content = append(root.Content, yamlString(key), value)
	}
	add("id", yamlString(rule.ID))
	add("title", yamlString(rule.Title))
	add("statement", yamlProse(rule.Statement))
	add("module", yamlString(rule.Module))
	add("entity", yamlString(rule.Entity))
	add("category", yamlString(rule.Category))
	add("status", yamlString(rule.Status))
	add("authority", yamlString(rule.Authority))
	add("mechanism", yamlStringPtr(rule.Mechanism))
	add("write_scope", yamlStringPtr(rule.WriteScope))
	add("enforcement", yamlEnforcementList(rule.Enforcement))
	add("refusal", yamlRefusal(rule.Refusal))
	add("fixture", yamlStringPtr(rule.Fixture))
	add("verification", yamlVerification(rule.Verification))
	add("expert-ref", yamlStringPtr(rule.ExpertRef))
	add("knowledge-ref", yamlStringPtr(rule.KnowledgeRef))
	add("stale_pointer", yamlProsePtr(rule.StalePointer))
	add("tie", yamlProsePtr(rule.Tie))

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, fmt.Errorf("encode front matter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close front-matter encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// yamlString is a string scalar; the emitter quotes it when the value could be
// read as another type or would change the surrounding structure.
func yamlString(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// yamlProse is a prose scalar: a literal block, so the emitter never reflows a
// sentence across lines and the file stays byte-stable.
func yamlProse(s string) *yaml.Node {
	node := yamlString(s)
	if s != "" {
		node.Style = yaml.LiteralStyle
	}
	return node
}

// yamlNull is the explicit null every absent field renders as.
func yamlNull() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}

func yamlStringPtr(p *string) *yaml.Node {
	if p == nil {
		return yamlNull()
	}
	return yamlString(*p)
}

func yamlProsePtr(p *string) *yaml.Node {
	if p == nil {
		return yamlNull()
	}
	return yamlProse(*p)
}

// yamlStrings renders a string list: nil is null (absent), a non-nil empty slice
// is `[]`. The two are distinct Go values, so the round-trip test needs both.
func yamlStrings(list []string) *yaml.Node {
	if list == nil {
		return yamlNull()
	}
	items := make([]*yaml.Node, 0, len(list))
	for _, s := range list {
		items = append(items, yamlString(s))
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func yamlEnforcementList(sites []Enforcement) *yaml.Node {
	if sites == nil {
		return yamlNull()
	}
	items := make([]*yaml.Node, 0, len(sites))
	for _, site := range sites {
		items = append(items, yamlMapping(
			"site", yamlString(site.Site),
			"layer", yamlString(site.Layer),
			"role", yamlString(site.Role),
			"pinned_by", yamlStringPtr(site.PinnedBy),
		))
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func yamlRefusal(refusal *Refusal) *yaml.Node {
	if refusal == nil {
		return yamlNull()
	}
	var http *yaml.Node
	if refusal.HTTP == nil {
		http = yamlNull()
	} else {
		http = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(*refusal.HTTP)}
	}
	return yamlMapping(
		"code", yamlString(refusal.Code),
		"constraint", yamlStrings(refusal.Constraint),
		"sentinel", yamlStringPtr(refusal.Sentinel),
		"http", http,
	)
}

func yamlVerification(verification Verification) *yaml.Node {
	return yamlMapping(
		"unit", yamlStrings(verification.Unit),
		"e2e", yamlStrings(verification.E2E),
		"gherkin", yamlStrings(verification.Gherkin),
	)
}

// yamlMapping builds a mapping node from alternating key/value nodes.
func yamlMapping(pairs ...any) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		value, _ := pairs[i+1].(*yaml.Node)
		node.Content = append(node.Content, yamlString(key), value)
	}
	return node
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
