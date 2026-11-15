package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// markdownHeader is the stable, timestamp-free preamble. Byte-identical output
// for identical input is a hard requirement: no clocks, no map iteration order.
const markdownHeader = `<!-- GENERATED — do not edit. -->
# Business rules

> **GENERATED — do not edit.** This file is a readable view of the canonical,
> hand-edited catalog ` + "`%s`" + `. Change the YAML, never this file, then
> regenerate:

` + "```sh" + `
go run ./cmd/spec gen --rules %s --out %s
` + "```" + `

The catalog's schema, enums and conventions live in ` + "`rules.schema.md`" + `;
the catalog's own invariants are checked by ` + "`go run ./cmd/spec check`" + `.

`

func runGen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules.yaml", "path to the canonical YAML catalog")
	outPath := fs.String("out", "docs/spec/business-rules.md", "path of the generated Markdown")
	diff := fs.Bool("diff", false, "compare instead of writing; exit 1 if the output would change")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "spec gen: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	catalog, err := loadCatalog(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec gen: %v\n", err)
		return 1
	}
	markdown := renderMarkdown(catalog, *rulesPath, *outPath)

	if *diff {
		existing, err := os.ReadFile(*outPath)
		switch {
		case err == nil && bytes.Equal(existing, markdown):
			fmt.Fprintf(stdout, "spec gen --diff: %s is up to date\n", *outPath)
			return 0
		case err == nil:
			fmt.Fprintf(stdout, "spec gen --diff: %s is stale — run `go run ./cmd/spec gen`\n", *outPath)
			return 1
		case os.IsNotExist(err):
			fmt.Fprintf(stdout, "spec gen --diff: %s does not exist — run `go run ./cmd/spec gen`\n", *outPath)
			return 1
		default:
			fmt.Fprintf(stderr, "spec gen: read %s: %v\n", *outPath, err)
			return 1
		}
	}

	if dir := filepath.Dir(*outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "spec gen: create %s: %v\n", dir, err)
			return 1
		}
	}
	if err := os.WriteFile(*outPath, markdown, 0o644); err != nil {
		fmt.Fprintf(stderr, "spec gen: write %s: %v\n", *outPath, err)
		return 1
	}
	fmt.Fprintf(stdout, "spec gen: wrote %s (%d rules)\n", *outPath, len(catalog))
	return 0
}

// renderMarkdown renders the whole file. Everything is sorted; none of the
// output depends on YAML ordering or Go map ordering.
func renderMarkdown(catalog Catalog, rulesPath, outPath string) []byte {
	rules := make([]Rule, len(catalog))
	copy(rules, catalog)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })

	var b strings.Builder
	fmt.Fprintf(&b, markdownHeader, displayPath(rulesPath), displayPath(rulesPath), displayPath(outPath))
	writeDashboard(&b, rules)
	writeRules(&b, rules)
	// Exactly one trailing newline keeps the file byte-stable and diff-friendly.
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func writeDashboard(b *strings.Builder, rules []Rule) {
	modules := map[string]int{}
	statuses := map[string]int{}
	categories := map[string]int{}
	authorities := map[string]int{}
	for _, r := range rules {
		modules[r.Module]++
		statuses[r.Status]++
		categories[r.Category]++
		authorities[r.Authority]++
	}

	fmt.Fprintf(b, "## Dashboard\n\n**%d rules** across %d modules.\n\n", len(rules), len(modules))
	writeCountTable(b, "By module", "Module", modules, len(rules))
	writeCountTable(b, "By status", "Status", statuses, len(rules))
	writeCountTable(b, "By category", "Category", categories, len(rules))
	writeCountTable(b, "By authority", "Authority", authorities, len(rules))
}

func writeCountTable(b *strings.Builder, heading, column string, counts map[string]int, total int) {
	fmt.Fprintf(b, "### %s\n\n| %s | Rules |\n| --- | ---: |\n", heading, column)
	for _, key := range sortedKeys(counts) {
		fmt.Fprintf(b, "| `%s` | %d |\n", mdCell(key), counts[key])
	}
	fmt.Fprintf(b, "| **total** | **%d** |\n\n", total)
}

func writeRules(b *strings.Builder, rules []Rule) {
	// Group module → entity. Rules arrive sorted by id, so each bucket is
	// already in id order.
	modules := map[string]map[string][]Rule{}
	for _, r := range rules {
		entities := modules[r.Module]
		if entities == nil {
			entities = map[string][]Rule{}
			modules[r.Module] = entities
		}
		entities[r.Entity] = append(entities[r.Entity], r)
	}

	moduleNames := make([]string, 0, len(modules))
	for name := range modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	for _, moduleName := range moduleNames {
		fmt.Fprintf(b, "## Module `%s`\n\n", mdCell(moduleName))
		entities := modules[moduleName]
		entityNames := make([]string, 0, len(entities))
		for name := range entities {
			entityNames = append(entityNames, name)
		}
		sort.Strings(entityNames)
		for _, entityName := range entityNames {
			fmt.Fprintf(b, "### Entity `%s`\n\n", mdCell(entityName))
			for _, rule := range entities[entityName] {
				writeRule(b, rule)
			}
		}
	}
}

func writeRule(b *strings.Builder, rule Rule) {
	fmt.Fprintf(b, "#### %s — %s\n\n", mdCell(rule.ID), mdCell(rule.Title))
	fmt.Fprintf(b, "`%s` · `%s` · authority `%s`\n\n", mdCell(rule.Status), mdCell(rule.Category), mdCell(rule.Authority))
	fmt.Fprintf(b, "%s\n\n", normalizeProse(rule.Statement))

	// Authority line. Mechanism only exists for database authority; scope is
	// optional outside it.
	authority := fmt.Sprintf("**Authority** `%s`", mdCell(rule.Authority))
	if rule.Mechanism != nil {
		authority += fmt.Sprintf(" · mechanism `%s`", mdCell(*rule.Mechanism))
	}
	if rule.Scope != nil {
		authority += fmt.Sprintf(" · scope `%s`", mdCell(*rule.Scope))
	}
	fmt.Fprintf(b, "%s\n\n", authority)

	if len(rule.Enforcement) > 0 {
		fmt.Fprint(b, "**Enforcement**\n\n| Site | Layer | Role | Pinned by |\n| --- | --- | --- | --- |\n")
		for _, site := range rule.Enforcement {
			pinned := "—"
			if site.PinnedBy != nil && *site.PinnedBy != "" {
				pinned = "`" + mdCell(*site.PinnedBy) + "`"
			}
			fmt.Fprintf(b, "| `%s` | `%s` | `%s` | %s |\n",
				mdCell(displayPath(site.Site)), mdCell(site.Layer), mdCell(site.Role), pinned)
		}
		fmt.Fprint(b, "\n")
	}

	if rule.Refusal != nil {
		fmt.Fprintf(b, "**Refusal** %s\n\n", refusalChain(rule.Refusal))
	}

	if unit, e2e := rule.Verification.Unit, rule.Verification.E2E; len(unit) > 0 || len(e2e) > 0 {
		fmt.Fprint(b, "**Verification**\n\n")
		for _, pointer := range unit {
			fmt.Fprintf(b, "- unit: `%s`\n", mdCell(displayPath(pointer)))
		}
		for _, pointer := range e2e {
			fmt.Fprintf(b, "- e2e: `%s`\n", mdCell(displayPath(pointer)))
		}
		fmt.Fprint(b, "\n")
	}

	if rule.ExpertRef != nil && *rule.ExpertRef != "" {
		fmt.Fprintf(b, "**Expert ref** `%s`\n\n", mdCell(displayPath(*rule.ExpertRef)))
	}

	// Notes are deliberately omitted unless a reviewer needs them: drift is a
	// content disagreement, stale-pointer notes are rot the report surfaces.
	if rule.Status == statusDrifted || strings.Contains(rule.Notes, stalePointerPrefix) {
		writeNotes(b, rule.Notes)
	}
}

func refusalChain(refusal *Refusal) string {
	code := "`" + mdCell(refusal.Code) + "`"
	if refusal.Code == "" {
		code = "_(no code)_"
	}

	constraints := "_(unnamed)_"
	names := make([]string, 0, len(refusal.Constraint))
	for _, name := range refusal.Constraint {
		names = append(names, "`"+mdCell(name)+"`")
	}
	if len(names) > 0 {
		constraints = strings.Join(names, ", ")
	}

	sentinel := "_(unmapped)_"
	if refusal.Sentinel != nil && *refusal.Sentinel != "" {
		sentinel = "`" + mdCell(*refusal.Sentinel) + "`"
	}

	http := "_(falls through to 500)_"
	if refusal.HTTP != nil {
		http = fmt.Sprintf("`%d`", *refusal.HTTP)
	}

	return fmt.Sprintf("%s → %s → %s → %s", code, constraints, sentinel, http)
}

func writeNotes(b *strings.Builder, notes string) {
	fmt.Fprint(b, "**Notes**\n\n")
	for _, line := range strings.Split(strings.TrimSpace(notes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Fprint(b, ">\n")
			continue
		}
		fmt.Fprintf(b, "> %s\n", line)
	}
	fmt.Fprint(b, "\n")
}

// mdCell keeps a value from breaking the surrounding Markdown table or heading.
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
