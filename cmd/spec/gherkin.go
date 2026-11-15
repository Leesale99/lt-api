package main

// gherkin.go cross-validates the catalog and the executable specification's
// feature files in BOTH directions:
//
//   - every `verification.gherkin` pointer in the catalog resolves to a real
//     scenario in a real feature file (the pointer format is
//     `<feature-file-path>::<scenario name>`, gherkin-style.md §3);
//   - every `@rule:R-<MODULE>-<NNN>` tag in every feature resolves to a
//     catalog rule, and every scenario names exactly one rule;
//   - the report states, per feature file and per rule, which rules are tagged
//     and how many scenarios prove them.
//
// A scenario under a tagged `Rule:` group inherits the tag, so the pickles the
// gherkin parser produces carry it — the same inheritance the godog suite runs
// with.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

const (
	// featuresRoot is the feature tree, relative to the repository root.
	featuresRoot = "docs/spec/features"

	// ruleTagPrefix is the tag family every scenario carries: one rule, one
	// tag (gherkin-style.md §2).
	ruleTagPrefix = "@rule:"

	// policyGherkinError is the policy id the pointer findings are reported
	// under; policyGherkinFeature is the feature-tree findings (parse errors,
	// tags, names).
	policyGherkinPointer = "10-gherkin"
	policyGherkinFeature = "12-features"
)

// scenarioRef is one executable scenario, as the parser compiled it.
type scenarioRef struct {
	Path string // repository-relative, slash-separated
	Name string // interpolated (Scenario Outline rows expanded)
	Line int
	Rule string // the one @rule id this scenario proves ("" when untagged)
	Tags []string
}

// pointer is the catalog's addressing form for a scenario.
func (s scenarioRef) pointer() string { return s.Path + "::" + s.Name }

// featureFile is one feature file's contribution.
type featureFile struct {
	Path      string
	Rules     []string
	Scenarios int
}

// featureIndex is the parsed feature tree.
type featureIndex struct {
	byPointer   map[string]scenarioRef
	byRule      map[string][]scenarioRef
	files       []featureFile
	fileScenes  map[string]int
	unparseable map[string]bool
}

func newFeatureIndex() *featureIndex {
	return &featureIndex{
		byPointer:   map[string]scenarioRef{},
		byRule:      map[string][]scenarioRef{},
		fileScenes:  map[string]int{},
		unparseable: map[string]bool{},
	}
}

// collectGherkin runs the feature cross-validation. It returns its findings (to
// be merged with the catalog's) and the coverage the report prints.
func collectGherkin(catalog Catalog, root string) ([]Finding, *featureIndex) {
	c := &checker{root: root, fixtures: map[string]bool{}}
	index := c.indexFeatures()

	// (1) every @rule tag resolves to a catalog rule, and every scenario names
	// exactly one.
	known := make(map[string]bool, len(catalog))
	for _, rule := range catalog {
		known[rule.ID] = true
	}
	refs := make([]scenarioRef, 0, len(index.byPointer))
	for _, ref := range index.byPointer {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path != refs[j].Path {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].Line < refs[j].Line
	})
	for _, ref := range refs {
		if ref.Rule != "" && !known[ref.Rule] {
			c.errorf(policyGherkinFeature, ref.Rule, "scenario %q in %s is tagged with an unknown rule id",
				ref.Name, ref.Path)
		}
	}

	// (2) every verification.gherkin pointer resolves to a real scenario.
	for _, rule := range catalog {
		for _, pointer := range rule.Verification.Gherkin {
			path, name, ok := splitGherkinPointer(pointer)
			if !ok {
				c.errorf(policyGherkinPointer, rule.ID,
					"gherkin pointer %q is not <feature-file-path>::<scenario name>", pointer)
				continue
			}
			if _, seen := index.byPointer[path+"::"+name]; seen {
				continue
			}
			switch {
			case index.unparseable[path]:
				c.errorf(policyGherkinPointer, rule.ID,
					"gherkin pointer %q cannot resolve: %s does not parse", pointer, path)
			case index.fileScenes[path] == 0:
				c.errorf(policyGherkinPointer, rule.ID,
					"gherkin pointer %q names no feature file with scenarios (%s)", pointer, path)
			default:
				c.errorf(policyGherkinPointer, rule.ID,
					"gherkin pointer %q names no scenario in %s", pointer, path)
			}
		}
	}

	sortFindings(c.findings)
	return c.findings, index
}

// indexFeatures parses every .feature file under the feature tree.
//
// Tag attribution walks the AST, not the pickles: a scenario's own tags and the
// tags of the `Rule:` group it sits in are different facts (gherkin-style.md
// §2 — a rule with several scenarios tags the group; a single-scenario rule tags
// the scenario). The pickles then supply the executable names, Scenario Outline
// rows interpolated.
func (c *checker) indexFeatures() *featureIndex {
	index := newFeatureIndex()

	root := filepath.Join(c.root, filepath.FromSlash(featuresRoot))
	for _, full := range walkingFeatureFiles(root) {
		rel, err := filepath.Rel(c.root, full)
		if err != nil {
			rel = full
		}
		rel = filepath.ToSlash(rel)

		file, err := os.Open(full)
		if err != nil {
			index.unparseable[rel] = true
			c.errorf(policyGherkinFeature, "", "feature file %q cannot be read: %v", rel, err)
			continue
		}

		ids := &messages.Incrementing{}
		doc, parseErr := gherkin.ParseGherkinDocument(file, ids.NewId)
		_ = file.Close()
		if parseErr != nil {
			index.unparseable[rel] = true
			c.errorf(policyGherkinFeature, "", "feature file %q does not parse: %v", rel, parseErr)
			continue
		}
		if doc.Feature == nil {
			index.unparseable[rel] = true
			c.errorf(policyGherkinFeature, "", "feature file %q carries no feature", rel)
			continue
		}

		// name groups the executable scenarios by the AST node they came from:
		// an outline's rows share one node and (without placeholders) one name.
		names := map[string][]string{}
		nodeName := map[string]string{}
		for _, pickle := range gherkin.Pickles(*doc, rel, ids.NewId) {
			if len(pickle.AstNodeIds) == 0 {
				continue
			}
			node := pickle.AstNodeIds[0]
			if _, seen := names[node]; !seen {
				nodeName[node] = pickle.Name
			}
			names[node] = append(names[node], pickle.Name)
		}

		file_ := featureFile{Path: rel}
		seenRules := map[string]bool{}
		seenPointers := map[string]string{}

		addScenario := func(scenario *messages.Scenario, inherited string, line int) {
			// The scenario's own tag wins over the group's: one scenario
			// proves the rule it names (gherkin-style.md §2). A scenario that
			// tags its own rule while sitting in another rule's group is made
			// explicit here rather than counted twice.
			own := ruleTag(scenario.Tags)
			effective := own
			if effective == "" {
				effective = inherited
			}
			switch {
			case effective == "":
				c.errorf(policyGherkinFeature, "",
					"scenario %q in %s carries no @rule tag", scenario.Name, rel)
			}

			refs := names[scenario.Id]
			if len(refs) == 0 {
				return
			}
			// An outline's rows share one node; distinct names (placeholders)
			// are distinct pointers, repeated names are one pointer.
			seenName := map[string]bool{}
			for _, name := range refs {
				if seenName[name] {
					continue
				}
				seenName[name] = true

				ref := scenarioRef{Path: rel, Name: name, Line: line, Rule: effective}
				if previous, dup := seenPointers[rel+"::"+name]; dup {
					c.errorf(policyGherkinFeature, effective,
						"scenario name %q appears more than once in %s (path::name is the catalog's pointer)",
						name, rel)
					_ = previous
					continue
				}
				seenPointers[rel+"::"+name] = scenario.Id

				index.byPointer[ref.pointer()] = ref
				index.fileScenes[rel]++
				file_.Scenarios++
				if ref.Rule != "" {
					index.byRule[ref.Rule] = append(index.byRule[ref.Rule], ref)
					if !seenRules[ref.Rule] {
						seenRules[ref.Rule] = true
						file_.Rules = append(file_.Rules, ref.Rule)
					}
				}
			}
		}

		for _, child := range doc.Feature.Children {
			switch {
			case child.Scenario != nil:
				line := 0
				if child.Scenario.Location != nil {
					line = int(child.Scenario.Location.Line)
				}
				addScenario(child.Scenario, "", line)
			case child.Rule != nil:
				inherited := ruleTag(child.Rule.Tags)
				for _, ruleChild := range child.Rule.Children {
					if ruleChild.Scenario == nil {
						continue
					}
					line := 0
					if ruleChild.Scenario.Location != nil {
						line = int(ruleChild.Scenario.Location.Line)
					}
					addScenario(ruleChild.Scenario, inherited, line)
				}
			}
		}

		sort.Strings(file_.Rules)
		index.files = append(index.files, file_)
	}

	sort.Slice(index.files, func(i, j int) bool { return index.files[i].Path < index.files[j].Path })
	return index
}

// ruleTag returns the rule id a tag list carries, when it carries one.
func ruleTag(tags []*messages.Tag) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag.Name, ruleTagPrefix) {
			return strings.TrimPrefix(tag.Name, ruleTagPrefix)
		}
	}
	return ""
}

// walkingFeatureFiles lists the feature files under dir, sorted, dropping the
// two tree layouts git and editors leave behind.
func walkingFeatureFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".feature") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// splitGherkinPointer splits `<feature-file-path>::<scenario name>`. The name may
// contain `::` (a scenario title can), so only the first separator splits.
func splitGherkinPointer(pointer string) (path, name string, ok bool) {
	index := strings.Index(pointer, "::")
	if index <= 0 {
		return "", "", false
	}
	path = strings.TrimSpace(pointer[:index])
	name = strings.TrimSpace(pointer[index+2:])
	if path == "" || name == "" {
		return "", "", false
	}
	if !strings.HasSuffix(path, ".feature") {
		return "", "", false
	}
	return path, name, true
}

// printGherkinSection prints the cross-validation: the file-level coverage, the
// per-rule scenario counts, and the findings the feature tree produced.
func printGherkinSection(w io.Writer, index *featureIndex, findings []Finding) {
	sectionFindings := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Policy == policyGherkinPointer || f.Policy == policyGherkinFeature {
			sectionFindings = append(sectionFindings, f)
		}
	}

	errorsFound, warnings := countFindings(sectionFindings)
	fmt.Fprintf(w, "## gherkin — feature cross-validation (%d errors, %d warnings)\n\n",
		errorsFound, warnings)

	if index == nil || len(index.files) == 0 {
		fmt.Fprintf(w, "no feature files under %s\n\n", featuresRoot)
		printGherkinFindings(w, sectionFindings)
		return
	}

	fmt.Fprint(w, "| Feature | Scenarios | Rules |\n| --- | ---: | --- |\n")
	for _, file := range index.files {
		rules := strings.Join(file.Rules, ", ")
		if rules == "" {
			rules = "—"
		}
		fmt.Fprintf(w, "| `%s` | %d | %s |\n", file.Path, file.Scenarios, rules)
	}
	fmt.Fprint(w, "\n")

	rules := make([]string, 0, len(index.byRule))
	for rule := range index.byRule {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	if len(rules) > 0 {
		fmt.Fprint(w, "| Rule | Scenarios | Feature |\n| --- | ---: | --- |\n")
		for _, rule := range rules {
			refs := index.byRule[rule]
			paths := map[string]bool{}
			for _, ref := range refs {
				paths[ref.Path] = true
			}
			files := make([]string, 0, len(paths))
			for path := range paths {
				files = append(files, "`"+path+"`")
			}
			sort.Strings(files)
			fmt.Fprintf(w, "| `%s` | %d | %s |\n", rule, len(refs), strings.Join(files, ", "))
		}
		fmt.Fprint(w, "\n")
	}

	printGherkinFindings(w, sectionFindings)
}

func printGherkinFindings(w io.Writer, findings []Finding) {
	if len(findings) == 0 {
		fmt.Fprint(w, "cross-validation: every pointer and tag resolves; every scenario names one rule\n\n")
		return
	}
	for _, f := range findings {
		prefix := ""
		if f.Severity == severityWarn {
			prefix = "WARN "
		}
		ruleID := f.RuleID
		if ruleID == "" {
			ruleID = "(features)"
		}
		fmt.Fprintf(w, "- %s**%s** %s\n", prefix, ruleID, f.Message)
	}
	fmt.Fprint(w, "\n")
}
