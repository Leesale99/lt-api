package main

// migrate.go owns the per-rule-file catalog format — one Markdown file per rule
// at docs/spec/rules/<module>/<ID>.md — and the one-shot `spec migrate` command
// that produces it from today's single-file catalog.
//
// NOTHING here is wired into the live loader yet: loadCatalog, check, gen, brief,
// queue and prove still read docs/spec/rules.yaml, so the tree stays green on the
// old format while the new one is introduced. The struct (RuleV2) lives here for
// the same reason and moves into catalog.go in the commit that switches the
// loader over.
//
// The file format
// ---------------
// A rule file is a strict YAML front matter between two `---` fences at the top
// of the file, followed by an optional Markdown body of authored prose:
//
//	---
//	<the machine fields, exactly the fields of RuleV2>
//	---
//
//	## Notes
//
//	<the residual human prose for this rule>
//
// Front matter decodes strictly (yaml.v3 KnownFields(true)) into exactly one YAML
// document, so a misspelled key is an error, `scope:` (the old name) is an error,
// and `notes:` is an error — notes are not a front-matter field any more. Body
// prose is not machine state; the only thing the loader reads out of it is the
// prose under the single `## Notes` heading, captured as the rule's Notes string
// so policies 5 (drifted ⇒ notes), 7 (a copy tied by a Tie note) and 8 (authority
// ≠ category default ⇒ notes) stay satisfiable.
//
// Canonical Notes form
// --------------------
// Notes, stale_pointer and tie are all stored as `normalizeProse` output: every
// whitespace run (including the folded YAML line wrapping authors used to control
// by hand) collapses to one space, and the value carries no leading/trailing
// whitespace. Rendering therefore writes one paragraph line per value and
// parsing re-normalizes, so parse(render(t)) == t holds by construction and an
// author's line wrapping can never change the decoded value.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// ruleFileFence delimits the front matter. It is matched at line start only,
	// so an indented `---` inside a block scalar can never close the block.
	ruleFileFence = "---"

	// ruleNotesHeading is the one body section the loader reads.
	ruleNotesHeading = "## Notes"

	// noteMarkerTie and noteMarkerStalePointer are today's in-prose markers. The
	// migration lifts them into the `tie:` and `stale_pointer:` fields; the
	// per-rule-file format has no in-prose markers.
	noteMarkerTie          = "Tie"
	noteMarkerStalePointer = "stale-pointer"
)

// RuleV2 is one rule of the per-rule-file catalog. It mirrors Rule field for
// field — same yaml tags, same pointer-for-nullable discipline — except:
//
//   - Scope is renamed WriteScope (`write_scope:`), which kills the collision
//     with run-scoping; the values are unchanged (row-local/cross-row/cross-table).
//   - StalePointer and Tie are promoted out of the folded notes prose into
//     first-class fields.
//   - Notes is not a front-matter field at all: it is captured from the body
//     prose under `## Notes`, so its tag is "-" and a `notes:` key in the front
//     matter is a strict-decoding error.
type RuleV2 struct {
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
	KnowledgeRef *string       `yaml:"knowledge-ref"`
	StalePointer *string       `yaml:"stale_pointer"`
	Tie          *string       `yaml:"tie"`
	Notes        string        `yaml:"-"`
}

// noteMarkerPattern matches today's in-prose note markers. Every occurrence of
// either marker in the canonical catalog is a marker (14 stale-pointer, 41 Tie);
// the labels are unique enough that no prose word ends in them.
var noteMarkerPattern = regexp.MustCompile(`(stale-pointer|Tie):`)

// transformRule maps a rule of today's single-file catalog onto the per-rule-file
// shape. Everything except the notes split is a verbatim copy; Scope becomes
// WriteScope, and the notes prose is split into stale_pointer, tie and the
// residual Notes.
func transformRule(old Rule) RuleV2 {
	stalePointer, tie, residual := splitNotes(old.Notes)
	return RuleV2{
		ID:           old.ID,
		Title:        old.Title,
		Statement:    old.Statement,
		Module:       old.Module,
		Entity:       old.Entity,
		Category:     old.Category,
		Status:       old.Status,
		Authority:    old.Authority,
		Mechanism:    old.Mechanism,
		WriteScope:   old.Scope,
		Enforcement:  old.Enforcement,
		Refusal:      old.Refusal,
		Fixture:      old.Fixture,
		Verification: old.Verification,
		ExpertRef:    old.ExpertRef,
		KnowledgeRef: old.KnowledgeRef,
		StalePointer: stalePointer,
		Tie:          tie,
		Notes:        residual,
	}
}

// splitNotes splits today's folded notes prose into the two structured notes and
// the residual human prose. The rules, in the order they apply:
//
//  1. The prose is normalized first (`normalizeProse`), so the author's YAML line
//     wrapping is irrelevant and a marker can never be split across lines.
//  2. Markers are found in order. A marker's segment runs to the next marker, or
//     to the end of the notes.
//  3. A `Tie:` segment is one sentence: it ends at the first ". " in the segment
//     (or at the end of the notes when the sentence is the last thing there). The
//     remainder stays in the residual prose — a Tie note is a tie *sentence*, and
//     the prose after it was never part of it.
//  4. A `stale-pointer:` segment is the whole rest of the segment: a rot note is
//     a paragraph of findings (000008's header overstates the gate ...; Second
//     finding: ...), not one sentence, and it is always the trailing note.
//  5. The marker label (`Tie:`, `stale-pointer:`) is stripped, and the remaining
//     value is trimmed. Empty values are dropped.
//  6. Several markers of one kind are joined in document order with one space,
//     which keeps each value a readable run of sentences.
//  7. The residual is the concatenation of everything the markers did not take,
//     re-normalized, with the orphaned parenthesis an extraction can leave behind
//     tidied up: a marker that sat inside a parenthetical (`(Tie: ...`) no longer
//     leaves a space after the opening parenthesis, and an opening parenthesis
//     whose whole parenthetical was extracted is dropped (the residual would
//     otherwise end mid-punctuation).
func splitNotes(notes string) (stalePointer, tie *string, residual string) {
	text := normalizeProse(notes)
	type hit struct {
		start, end int
		kind       string
	}
	var hits []hit
	for _, m := range noteMarkerPattern.FindAllStringSubmatchIndex(text, -1) {
		hits = append(hits, hit{start: m[0], end: m[1], kind: text[m[2]:m[3]]})
	}

	var staleParts, tieParts, residualParts []string
	cursor := 0
	for i, h := range hits {
		segEnd := len(text)
		if i+1 < len(hits) {
			segEnd = hits[i+1].start
		}
		residualParts = append(residualParts, text[cursor:h.start])
		segment := text[h.start:segEnd]
		if h.kind == noteMarkerTie {
			if cut := strings.Index(segment, ". "); cut >= 0 {
				residualParts = append(residualParts, text[h.start+cut+1:segEnd])
				segment = segment[:cut+1]
			}
		}
		value := strings.TrimSpace(strings.TrimPrefix(segment, h.kind+":"))
		if value != "" {
			if h.kind == noteMarkerTie {
				tieParts = append(tieParts, value)
			} else {
				staleParts = append(staleParts, value)
			}
		}
		cursor = segEnd
	}
	residualParts = append(residualParts, text[cursor:])

	residual = normalizeProse(strings.Join(residualParts, " "))
	residual = strings.ReplaceAll(residual, "( ", "(")
	if strings.HasSuffix(residual, " (") {
		residual = strings.TrimSpace(strings.TrimSuffix(residual, "("))
	}
	return joinNoteParts(staleParts), joinNoteParts(tieParts), residual
}

// joinNoteParts is the single-kind join: nil when a rule has no marker of that
// kind, otherwise the parts joined with one space in document order.
func joinNoteParts(parts []string) *string {
	if len(parts) == 0 {
		return nil
	}
	joined := strings.Join(parts, " ")
	return &joined
}

// renderRuleFile renders one rule file: the front-matter fences, the machine
// fields, and — only when there is residual prose — a `## Notes` body. The result
// always has exactly one trailing newline (render.go's finishMarkdown), and a rule
// with no residual prose is front matter only.
func renderRuleFile(rule RuleV2) ([]byte, error) {
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
// to reflect.DeepEqual and therefore to the round-trip gate).
func ruleFrontMatter(rule RuleV2) ([]byte, error) {
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
// is `[]`. The two are distinct Go values, so the round-trip gate needs both.
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

// parseRuleFile parses one rule file: a strict, single-document YAML front matter
// between the fences, plus the Notes prose from the body. Strict decoding is the
// point — it is what makes `scope:` (the old name) and `notes:` (a field that no
// longer exists) hard errors instead of silently dropped keys.
func parseRuleFile(data []byte, name string) (RuleV2, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != ruleFileFence {
		return RuleV2{}, fmt.Errorf("parse rule file %s: the file must start with a %q front-matter fence",
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
		return RuleV2{}, fmt.Errorf("parse rule file %s: the front matter has no closing %q fence",
			name, ruleFileFence)
	}

	front := strings.Join(lines[1:closing], "\n")
	var rule RuleV2
	decoder := yaml.NewDecoder(strings.NewReader(front))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rule); err != nil {
		return RuleV2{}, fmt.Errorf("parse rule file %s: front matter: %w", name, err)
	}
	// The front matter is exactly one YAML document, for the same reason the
	// single-file catalog is: a second `---` document would be dropped silently.
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return RuleV2{}, fmt.Errorf("parse rule file %s: front matter must be exactly one YAML "+
			"document: %w", name, err)
	default:
		return RuleV2{}, fmt.Errorf("parse rule file %s: front matter must be exactly one YAML "+
			"document — found a second one", name)
	}

	notes, err := notesFromBody(lines[closing+1:])
	if err != nil {
		return RuleV2{}, fmt.Errorf("parse rule file %s: %w", name, err)
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

// ruleDiff names the RuleV2 fields whose values differ, so the round-trip gate
// can report "R-GAME-001 failed round-trip in field(s): tie" instead of a wall of
// struct dumps.
func ruleDiff(want, got RuleV2) []string {
	var diff []string
	wantValue, gotValue := reflect.ValueOf(want), reflect.ValueOf(got)
	typ := wantValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if reflect.DeepEqual(wantValue.Field(i).Interface(), gotValue.Field(i).Interface()) {
			continue
		}
		name := field.Tag.Get("yaml")
		if name == "" || name == "-" {
			name = strings.ToLower(field.Name)
		}
		diff = append(diff, name)
	}
	return diff
}

// runMigrate implements `spec migrate`: read the single-file catalog with the
// existing strict loader, transform every rule, prove the round trip for every
// rule BEFORE writing anything, and only then write the per-rule files.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules.yaml", "path to the single-file YAML catalog to convert")
	outDir := fs.String("out", "docs/spec/rules", "directory to write the per-rule files into")
	force := fs.Bool("force", false, "write into a non-empty target directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "spec migrate: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	catalog, err := loadCatalog(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec migrate: %v\n", err)
		return 1
	}
	if len(catalog) == 0 {
		fmt.Fprintf(stderr, "spec migrate: %s decoded zero rules\n", *rulesPath)
		return 1
	}

	if !*force {
		entries, err := os.ReadDir(*outDir)
		switch {
		case err == nil && len(entries) > 0:
			fmt.Fprintf(stderr, "spec migrate: target %s is not empty — refusing to write "+
				"(use --force to overwrite)\n", *outDir)
			return 1
		case err != nil && !errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(stderr, "spec migrate: read target %s: %v\n", *outDir, err)
			return 1
		}
	}

	type migratedRule struct {
		rule RuleV2
		file []byte
	}
	migrated := make([]migratedRule, 0, len(catalog))
	moduleTotal := make(map[string]int, len(moduleEnum))
	moduleOK := make(map[string]int, len(moduleEnum))
	seen := make(map[string]string, len(catalog))
	// diagnostics records the rules whose old notes prose was claimed in full by
	// the structured fields: they migrate to a file with no body at all, which is
	// legal but leaves nothing for a policy that wants an explanation (5, 7, 8).
	// The real catalog has one such rule, so the migration says its name instead of
	// letting the next reader discover the empty body in review.
	var diagnostics []string
	failures := 0

	for _, old := range catalog {
		rule := transformRule(old)
		if _, ok := moduleEnum[rule.Module]; !ok {
			fmt.Fprintf(stderr, "spec migrate: %s: module %q is not in the frozen module enum\n",
				rule.ID, rule.Module)
			return 1
		}
		key := rule.Module + "/" + rule.ID
		if other, ok := seen[key]; ok {
			fmt.Fprintf(stderr, "spec migrate: %s and %s would both be written to %s.md\n",
				other, rule.ID, key)
			return 1
		}
		seen[key] = rule.ID
		moduleTotal[rule.Module]++

		file, err := renderRuleFile(rule)
		if err != nil {
			fmt.Fprintf(stderr, "spec migrate: %s: %v\n", rule.ID, err)
			return 1
		}
		reparsed, err := parseRuleFile(file, key+".md")
		if err != nil {
			fmt.Fprintf(stderr, "spec migrate: %s failed round-trip: %v\n", rule.ID, err)
			failures++
			continue
		}
		if diff := ruleDiff(rule, reparsed); len(diff) > 0 {
			fmt.Fprintf(stderr, "spec migrate: %s failed round-trip in field(s): %s\n",
				rule.ID, strings.Join(diff, ", "))
			failures++
			continue
		}
		moduleOK[rule.Module]++
		if rule.Notes == "" && strings.TrimSpace(old.Notes) != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: its whole notes prose is now in "+
				"stale_pointer/tie, so the file has no body — write body prose by hand where a "+
				"policy needs notes", rule.ID))
		}
		migrated = append(migrated, migratedRule{rule: rule, file: file})
	}

	if failures > 0 {
		fmt.Fprintf(stderr, "spec migrate: %d of %d rules failed the round-trip gate — nothing "+
			"was written\n", failures, len(catalog))
		return 1
	}

	for _, module := range catalogModules() {
		if moduleTotal[module] == 0 {
			continue
		}
		fmt.Fprintf(stdout, "  %-9s %3d/%-3d round-trip ok\n",
			module, moduleOK[module], moduleTotal[module])
	}
	for _, line := range diagnostics {
		fmt.Fprintf(stdout, "note: %s\n", line)
	}
	fmt.Fprintf(stdout, "%d/%d round-trip ok\n", len(migrated), len(catalog))

	sort.Slice(migrated, func(i, j int) bool {
		if migrated[i].rule.Module != migrated[j].rule.Module {
			return migrated[i].rule.Module < migrated[j].rule.Module
		}
		return migrated[i].rule.ID < migrated[j].rule.ID
	})
	for _, m := range migrated {
		dir := filepath.Join(*outDir, m.rule.Module)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "spec migrate: create %s: %v\n", dir, err)
			return 1
		}
		path := filepath.Join(dir, m.rule.ID+".md")
		if err := os.WriteFile(path, m.file, 0o644); err != nil {
			fmt.Fprintf(stderr, "spec migrate: write %s: %v\n", path, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "wrote %d rule files to %s\n", len(migrated), *outDir)
	return 0
}
