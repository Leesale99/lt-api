package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The generated catalog is exactly one artifact: a thin dashboard. The catalog
// itself is authored — one Markdown file per rule under the rules directory
// (docs/spec/rules/<module>/<ID>.md) — so there are no generated per-module
// views and nothing to keep in sync beside the dashboard. `gen --diff` gates
// that one file.
//
// markdownHeader is the dashboard's stable, timestamp-free preamble.
// Byte-identical output for identical input is a hard requirement: no clocks,
// no map iteration order.
const markdownHeader = `<!-- GENERATED — do not edit. -->
# Business rules — dashboard

> **GENERATED — do not edit.** This dashboard is derived from the per-rule files
> under ` + "`%s`" + `: one authored Markdown file per rule. Those files are the
> canonical catalog; the dashboard is only a generated summary over them.
>
> Never hand-edit the dashboard. Change the rule file, then regenerate with
> ` + "`make spec/gen`" + ` (or the command below):

` + "```sh" + `
go run ./cmd/spec gen --rules %s --out %s
` + "```" + `

The catalog's schema, enums and conventions live in ` + "`rules.schema.md`" + `;
the catalog's own invariants are checked by ` + "`go run ./cmd/spec check`" + `.

`

// artifact is one generated file: where it goes and the exact bytes it carries.
type artifact struct {
	path    string
	content []byte
}

// runGen writes the dashboard, or — with --diff — compares it against the tree
// without writing anything.
func runGen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules", "path to the per-rule-file catalog directory")
	outPath := fs.String("out", "docs/spec/business-rules.md", "path of the generated dashboard")
	diff := fs.Bool("diff", false, "compare instead of writing; exit 1 if the dashboard would change")
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

	artifacts := renderArtifacts(catalog, *rulesPath, *outPath)
	if *diff {
		return diffArtifacts(artifacts, stdout, stderr)
	}

	for _, a := range artifacts {
		if dir := filepath.Dir(a.path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fmt.Fprintf(stderr, "spec gen: create %s: %v\n", dir, err)
				return 1
			}
		}
		if err := os.WriteFile(a.path, a.content, 0o644); err != nil {
			fmt.Fprintf(stderr, "spec gen: write %s: %v\n", a.path, err)
			return 1
		}
	}

	fmt.Fprintf(stdout, "spec gen: wrote %s (%d rules)\n", displayPath(*outPath), len(catalog))
	return 0
}

// renderArtifacts renders the one generated artifact: the dashboard. The
// per-rule files under the rules directory are authored and never generated, so
// rendering cannot produce a second file to keep in sync.
func renderArtifacts(catalog Catalog, rulesPath, outPath string) []artifact {
	return []artifact{{
		path:    outPath,
		content: renderDashboard(sortedRules(catalog), rulesPath, outPath),
	}}
}

// diffArtifacts compares every generated file against the tree. A stale or
// missing file is a change. The command never writes in diff mode, so it never
// deletes either.
func diffArtifacts(artifacts []artifact, stdout, stderr io.Writer) int {
	stale := 0
	for _, a := range artifacts {
		existing, err := os.ReadFile(a.path)
		switch {
		case err == nil && bytes.Equal(existing, a.content):
			fmt.Fprintf(stdout, "spec gen --diff: %s is up to date\n", displayPath(a.path))
		case err == nil:
			fmt.Fprintf(stdout, "spec gen --diff: %s is stale — run `go run ./cmd/spec gen`\n", displayPath(a.path))
			stale++
		case os.IsNotExist(err):
			fmt.Fprintf(stdout, "spec gen --diff: %s does not exist — run `go run ./cmd/spec gen`\n", displayPath(a.path))
			stale++
		default:
			fmt.Fprintf(stderr, "spec gen: read %s: %v\n", displayPath(a.path), err)
			return 1
		}
	}
	if stale > 0 {
		return 1
	}
	return 0
}

// renderDashboard renders the front door: the totals the catalog is read by and
// one link per module into the authored rule files.
func renderDashboard(rules []Rule, rulesPath, outPath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, markdownHeader, displayPath(rulesPath), displayPath(rulesPath), displayPath(outPath))
	writeDashboard(&b, rules, rulesPath, outPath)
	return finishMarkdown(&b)
}

func writeDashboard(b *strings.Builder, rules []Rule, rulesPath, outPath string) {
	modules := map[string]int{}
	statuses := map[string]int{}
	for _, r := range rules {
		modules[r.Module]++
		statuses[r.Status]++
	}

	fmt.Fprintf(b, "## Dashboard\n\n**%d rules** across %d modules — %s.\n\n",
		len(rules), len(modules), briefStatusCounts(rules))
	writeCountTable(b, "By status", "Status", statuses, len(rules))
	writeCountTable(b, "By module", "Module", modules, len(rules))

	// The index. Every module's rules are authored at rules/<module>/<ID>.md, so
	// every row links straight into that directory.
	fmt.Fprint(b, "### Rule files\n\n")
	fmt.Fprintf(b, "Every rule is authored in its own file, `<module>/<ID>.md`, under `%s`.\n\n",
		displayPath(rulesPath))
	fmt.Fprint(b, "| Module | Rules | Files |\n| --- | ---: | --- |\n")
	for _, module := range catalogModules() {
		fmt.Fprintf(b, "| `%s` | %d | [%s](%s) |\n",
			mdCell(module), modules[module], mdCell(module), moduleLink(outPath, rulesPath, module))
	}
	fmt.Fprintf(b, "| **total** | **%d** | |\n\n", len(rules))
}

// moduleLink is the dashboard's relative link into one module's rule directory
// (`rules/<module>/`), relative to whatever path the dashboard is written to so
// the link stays valid wherever the docs tree is checked out.
func moduleLink(outPath, rulesPath, module string) string {
	target := filepath.Join(rulesPath, module)
	if rel, err := filepath.Rel(filepath.Dir(outPath), target); err == nil {
		target = rel
	}
	return filepath.ToSlash(filepath.Clean(target)) + "/"
}

func writeCountTable(b *strings.Builder, heading, column string, counts map[string]int, total int) {
	fmt.Fprintf(b, "### %s\n\n| %s | Rules |\n| --- | ---: |\n", heading, column)
	for _, key := range sortedKeys(counts) {
		fmt.Fprintf(b, "| `%s` | %d |\n", mdCell(key), counts[key])
	}
	fmt.Fprintf(b, "| **total** | **%d** |\n\n", total)
}

// writeNotes renders a rule's notes as a block quote.
func writeNotes(b *strings.Builder, notes string) {
	writeLabeledNote(b, "Notes", notes)
}

// writeStructuredNotes renders the two note fields that were in-prose markers in
// the retired catalog. A caller that already rendered the Notes body (a brief's
// authority-deviation reason, for one) calls this alone, so nothing is lost and
// nothing is printed twice.
func writeStructuredNotes(b *strings.Builder, rule Rule) {
	if rule.StalePointer != nil {
		writeLabeledNote(b, "Stale pointer", *rule.StalePointer)
	}
	if rule.Tie != nil {
		writeLabeledNote(b, "Tie", *rule.Tie)
	}
}

// writeLabeledNote renders one prose block under a bold label as a block quote.
func writeLabeledNote(b *strings.Builder, label, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	fmt.Fprintf(b, "**%s**\n\n", label)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Fprint(b, ">\n")
			continue
		}
		fmt.Fprintf(b, "> %s\n", escapeQuoteLine(line))
	}
	fmt.Fprint(b, "\n")
}
