package main

// render.go owns the Markdown rendering primitives every generated artifact
// shares: the table-cell escape (mdCell), the prose fold (normalizeProse), the
// pointer display form (displayPath), the quote-line escape and its list-marker
// pattern, the one-trailing-newline finish (finishMarkdown), and the catalog
// orderings plus the frozen module enumeration (moduleEnum, catalogModules).
// gen, work and check all render from here, so an artifact stays
// byte-identical whichever command wrote it.

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// moduleEnum is the frozen module set, docs/spec/rules.schema.md §1. It lives
// with the renderers because every generated view is keyed by a module.
// moduleOrder is the frozen module enum in declaration order — the order the
// schema's tables and the catalog list modules in. The per-rule-file loader sorts
// the catalog by it, so catalog order never depends on readdir order.
var moduleOrder = []string{"schedule", "game", "identity", "ledger", "kernel", "platform"}

var moduleEnum = enum(moduleOrder...)

// catalogModules returns the six module names, sorted, straight from the frozen
// enum so a module can never exist in the views and not in the checker.
func catalogModules() []string {
	modules := make([]string, 0, len(moduleEnum))
	for name := range moduleEnum {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	return modules
}

// sortedRules copies the catalog in id order. Every work order is deterministic:
// none of the output depends on YAML or map ordering.
func sortedRules(catalog Catalog) []Rule {
	rules := make([]Rule, len(catalog))
	copy(rules, catalog)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules
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

// orderedListMarker matches a leading `1.`/`12.` list marker; only the dot is
// escaped so the note reads as prose inside the quote, not a nested list.
var orderedListMarker = regexp.MustCompile(`^([0-9]+)\.(?:[ \t]|$)`)

// escapeQuoteLine neutralizes exactly the leading characters that would change
// the Markdown structure of the block quote — a nested quote (`>`), a heading
// (`#`), and a list marker (`-`, `*`, `+`, `1.`). Ordinary prose is untouched.
func escapeQuoteLine(line string) string {
	switch {
	case strings.HasPrefix(line, ">"), strings.HasPrefix(line, "#"):
		return `\` + line
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "), strings.HasPrefix(line, "+ "),
		line == "-", line == "*", line == "+":
		return `\` + line
	}
	if loc := orderedListMarker.FindStringSubmatchIndex(line); loc != nil {
		dot := loc[3] // end of the digit group, i.e. the `.`
		return line[:dot] + `\` + line[dot:]
	}
	return line
}

// finishMarkdown keeps every generated artifact byte-stable: exactly one
// trailing newline.
func finishMarkdown(b *strings.Builder) []byte {
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

// mdCell keeps a value from breaking the surrounding Markdown table or heading.
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
