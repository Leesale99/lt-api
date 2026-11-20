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

// The generated catalog is split in two: a dashboard (the front door: totals,
// the per-module counts and the index) and one view per module (the full rule
// blocks). Both are generated from the same catalog by the same run, and
// `gen --diff` gates both.
const (
	// viewsDirName is the per-module view directory, sibling of the dashboard.
	viewsDirName = "views"
)

// markdownHeader is the dashboard's stable, timestamp-free preamble.
// Byte-identical output for identical input is a hard requirement: no clocks,
// no map iteration order.
const markdownHeader = `<!-- GENERATED — do not edit. -->
# Business rules — dashboard

> **GENERATED — do not edit.** This file is the dashboard and index over the
> per-module views in ` + "`views/`" + `. Both are readable views of the canonical,
> hand-edited catalog ` + "`%s`" + `.
>
> Change the YAML, never these files, then regenerate:

` + "```sh" + `
go run ./cmd/spec gen --rules %s --out %s
` + "```" + `

The catalog's schema, enums and conventions live in ` + "`rules.schema.md`" + `;
the catalog's own invariants are checked by ` + "`go run ./cmd/spec check`" + `.

`

// viewHeader is one module view's preamble. It carries the same generated
// marker and the same regeneration command as the dashboard.
const viewHeader = `<!-- GENERATED — do not edit. -->
# Business rules — ` + "`%s`" + `

> **GENERATED — do not edit.** This is the ` + "`%s`" + ` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is ` + "`%s`" + `.
>
> Change the YAML, never this file, then regenerate from the dashboard:

` + "```sh" + `
go run ./cmd/spec gen --rules %s --out %s
` + "```" + `

The rules themselves live in ` + "`../features/%s/`" + `; the format authority is
` + "`rules.schema.md`" + ` and the checker is ` + "`go run ./cmd/spec check`" + `.

`

// artifact is one generated file: where it goes and the exact bytes it carries.
type artifact struct {
	path    string
	content []byte
}

// runGen writes the dashboard and every module view, or — with --diff —
// compares both against the tree without writing anything.
func runGen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules.yaml", "path to the canonical YAML catalog")
	outPath := fs.String("out", "docs/spec/business-rules.md", "path of the generated dashboard (the views land in a views/ directory beside it)")
	diff := fs.Bool("diff", false, "compare instead of writing; exit 1 if any generated file would change")
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

	// Write mode never deletes: an orphan view is reported and left in place,
	// because removing a file is a human decision. It is not fatal here —
	// `make spec/gen` writes, `make spec/diff` gates — but `--diff` fails on it.
	orphans, err := viewOrphans(artifacts)
	if err != nil {
		fmt.Fprintf(stderr, "spec gen: %v\n", err)
		return 1
	}
	for _, orphan := range orphans {
		fmt.Fprintf(stderr, "spec gen: %s is an orphan the views directory does not render — left in place; remove it\n",
			displayPath(orphan))
	}

	fmt.Fprintf(stdout, "spec gen: wrote %s (%d rules)\n", displayPath(*outPath), len(catalog))
	fmt.Fprintf(stdout, "spec gen: wrote %d module views in %s\n",
		len(artifacts)-1, displayPath(viewsDir(*outPath)))
	return 0
}

// viewsDir is the view directory that belongs to a dashboard path.
func viewsDir(outPath string) string {
	return filepath.Join(filepath.Dir(outPath), viewsDirName)
}

// renderArtifacts renders the dashboard and the six module views. Every module
// gets a view, even the ones with no rule yet, so the dashboard's index links
// never dangle.
func renderArtifacts(catalog Catalog, rulesPath, outPath string) []artifact {
	rules := sortedRules(catalog)

	artifacts := make([]artifact, 0, 1+len(catalogModules()))
	artifacts = append(artifacts, artifact{
		path:    outPath,
		content: renderDashboard(rules, rulesPath, outPath),
	})
	for _, module := range catalogModules() {
		artifacts = append(artifacts, artifact{
			path:    filepath.Join(viewsDir(outPath), module+".md"),
			content: renderView(rules, module, rulesPath, outPath),
		})
	}
	return artifacts
}

// diffArtifacts compares every generated file against the tree. A stale or
// missing file is a change, and so is an orphan in the views directory — a file
// this run does not render (a renamed module's old view, a hand-written note).
// Comparing only the artifacts would make the orphan invisible. The command
// never writes in diff mode, so it never deletes either.
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

	orphans, err := viewOrphans(artifacts)
	if err != nil {
		fmt.Fprintf(stderr, "spec gen: %v\n", err)
		return 1
	}
	for _, orphan := range orphans {
		fmt.Fprintf(stdout, "spec gen --diff: %s is an orphan the views directory does not render — remove it (gen never deletes files)\n",
			displayPath(orphan))
		stale++
	}

	if stale > 0 {
		return 1
	}
	return 0
}

// viewOrphans lists the entries under the views directory that this run does
// not render, sorted for deterministic output. Dotfiles are skipped: they are
// never views, and an OS dropping like .DS_Store must not fail the diff. The
// directory is the parent of the rendered `views/<module>.md` artifacts; when it
// does not exist, there is nothing to report (the missing artifacts already fail
// the diff).
func viewOrphans(artifacts []artifact) ([]string, error) {
	dir := ""
	for _, a := range artifacts {
		if filepath.Base(filepath.Dir(a.path)) == viewsDirName {
			dir = filepath.Dir(a.path)
			break
		}
	}
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", displayPath(dir), err)
	}

	rendered := make(map[string]bool, len(artifacts))
	for _, a := range artifacts {
		rendered[filepath.Clean(a.path)] = true
	}
	var orphans []string
	for _, entry := range entries {
		// Dotfiles are OS/editor droppings (.DS_Store, swap files), not views:
		// an ignored one must not fail the diff. Same rule as the source walk in
		// annotations.go.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		if !rendered[filepath.Clean(full)] {
			orphans = append(orphans, full)
		}
	}
	sort.Strings(orphans)
	return orphans, nil
}

// renderDashboard renders the front door: totals, the counts the catalog is
// read by, and the index into the module views.
func renderDashboard(rules []Rule, rulesPath, outPath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, markdownHeader, displayPath(rulesPath), displayPath(rulesPath), displayPath(outPath))
	writeDashboard(&b, rules)
	return finishMarkdown(&b)
}

// renderView renders one module's full rule blocks — the same block format the
// generated catalog has always used.
func renderView(rules []Rule, module, rulesPath, outPath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, viewHeader, mdCell(module), mdCell(module),
		displayPath(rulesPath), displayPath(rulesPath), displayPath(outPath), mdCell(module))

	moduleRules := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.Module == module {
			moduleRules = append(moduleRules, rule)
		}
	}
	if len(moduleRules) == 0 {
		fmt.Fprintf(&b, "## Module `%s`\n\n_(no rule in this module yet)_\n\n", mdCell(module))
		return finishMarkdown(&b)
	}

	fmt.Fprintf(&b, "**%d %s** — %s.\n\n",
		len(moduleRules), plural(len(moduleRules), "rule", "rules"), briefStatusCounts(moduleRules))
	writeModule(&b, module, moduleRules)
	return finishMarkdown(&b)
}

func writeDashboard(b *strings.Builder, rules []Rule) {
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

	// The index. Every module gets a view, so every row links.
	fmt.Fprint(b, "### Module views\n\n| Module | Rules | View |\n| --- | ---: | --- |\n")
	for _, module := range catalogModules() {
		view := viewsDirName + "/" + module + ".md"
		fmt.Fprintf(b, "| `%s` | %d | [`%s`](%s) |\n", mdCell(module), modules[module], view, view)
	}
	fmt.Fprintf(b, "| **total** | **%d** | |\n\n", len(rules))
}

func writeCountTable(b *strings.Builder, heading, column string, counts map[string]int, total int) {
	fmt.Fprintf(b, "### %s\n\n| %s | Rules |\n| --- | ---: |\n", heading, column)
	for _, key := range sortedKeys(counts) {
		fmt.Fprintf(b, "| `%s` | %d |\n", mdCell(key), counts[key])
	}
	fmt.Fprintf(b, "| **total** | **%d** |\n\n", total)
}

// writeModule prints one module's rules grouped entity → id. The rules arrive
// sorted by id, so each bucket is already in id order.
func writeModule(b *strings.Builder, moduleName string, rules []Rule) {
	fmt.Fprintf(b, "## Module `%s`\n\n", mdCell(moduleName))

	entities := map[string][]Rule{}
	for _, r := range rules {
		entities[r.Entity] = append(entities[r.Entity], r)
	}
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

	// The knowledge-base note sits beside expert-ref and mirrors its line shape:
	// expert-ref says where the rule was specified, knowledge-ref says which note
	// explains it. Both are optional and neither replaces the other.
	if rule.KnowledgeRef != nil && *rule.KnowledgeRef != "" {
		fmt.Fprintf(b, "**Knowledge ref** `%s`\n\n", mdCell(displayPath(*rule.KnowledgeRef)))
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
		fmt.Fprintf(b, "> %s\n", escapeQuoteLine(line))
	}
	fmt.Fprint(b, "\n")
}

// mdCell keeps a value from breaking the surrounding Markdown table or heading.
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
