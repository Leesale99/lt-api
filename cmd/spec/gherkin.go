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
//
// The feature-tree parse itself (the index, the scenario references, the pointer
// forms) lives in features.go; this file owns the two policies that read it.

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	// policyGherkinError is the policy id the pointer findings are reported
	// under; policyGherkinFeature is the feature-tree findings (parse errors,
	// tags, names).
	policyGherkinPointer = "10-gherkin"
	policyGherkinFeature = "12-features"
)

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
		subject := f.Subject
		if subject == "" {
			subject = "(features)"
		}
		fmt.Fprintf(w, "- %s**%s** %s\n", prefix, subject, f.Message)
	}
	fmt.Fprint(w, "\n")
}
