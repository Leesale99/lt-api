package main

// features.go owns the ONE parse of the executable specification's feature tree:
// the scenario references, the per-file and per-rule index, and the pointer
// forms the catalog addresses scenarios by. gherkin.go keeps the gherkin policies
// (10-gherkin, 12-features) that consume this index; check, brief, queue, prove
// and oracle all read the same parse through it, so two commands can never
// disagree about which scenario exists.

import (
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
)

// scenarioRef is one executable scenario, as the parser compiled it.
type scenarioRef struct {
	Path string // repository-relative, slash-separated
	Name string // interpolated (Scenario Outline rows expanded)
	Line int
	Rule string // the one @rule id this scenario proves ("" when untagged)
	Tags []string
	// Steps is the scenario's step text, so a policy that reads the sentence
	// (13-vocabulary) uses this parse instead of reading the tree again. An
	// outline's rows that share one name contribute each row's steps once.
	Steps []scenarioStep
}

// scenarioStep is one step as a vocabulary policy reads it: the line's text plus
// the literal argument blocks the step carries (a docstring body, or the cells
// of a data table). Both halves are part of the step, so both are scanned.
type scenarioStep struct {
	Text      string
	DocString string   // "" when the step carries no docstring
	Cells     []string // data-table cell values, row-major
}

// signature is the identity a step is deduplicated by: two steps with the same
// text and the same argument are one statement, so an outline whose rows differ
// only in a placeholder scanned the same way reports once.
func (s scenarioStep) signature() string {
	return s.Text + "\x00" + s.DocString + "\x00" + strings.Join(s.Cells, "\x01")
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
	byPointer map[string]scenarioRef
	byRule    map[string][]scenarioRef
	// scenarios is every scenario in parse order: the deterministic walk order
	// for a policy that visits the tree (a map iteration is not one).
	scenarios   []scenarioRef
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

		// pickles groups the executable scenarios by the AST node they came
		// from: an outline's rows share one node and (without placeholders)
		// one name, and each pickle carries the steps its row substituted.
		pickles := map[string][]pickleScenario{}
		for _, pickle := range gherkin.Pickles(*doc, rel, ids.NewId) {
			if len(pickle.AstNodeIds) == 0 {
				continue
			}
			node := pickle.AstNodeIds[0]
			pickles[node] = append(pickles[node], pickleScenario{
				name:  pickle.Name,
				steps: indexedSteps(pickle.Steps),
			})
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

			refs := pickles[scenario.Id]
			if len(refs) == 0 {
				return
			}
			// An outline's rows share one node; distinct names (placeholders)
			// are distinct pointers, repeated names are one pointer whose steps
			// are the union of the rows' steps.
			byName := map[string][]scenarioStep{}
			order := make([]string, 0, len(refs))
			for _, pickle := range refs {
				if _, seen := byName[pickle.name]; !seen {
					order = append(order, pickle.name)
				}
				byName[pickle.name] = appendUniqueSteps(byName[pickle.name], pickle.steps)
			}
			for _, name := range order {
				ref := scenarioRef{Path: rel, Name: name, Line: line, Rule: effective, Steps: byName[name]}
				if previous, dup := seenPointers[rel+"::"+name]; dup {
					c.errorf(policyGherkinFeature, effective,
						"scenario name %q appears more than once in %s (path::name is the catalog's pointer)",
						name, rel)
					_ = previous
					continue
				}
				seenPointers[rel+"::"+name] = scenario.Id

				index.byPointer[ref.pointer()] = ref
				index.scenarios = append(index.scenarios, ref)
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

// pickleScenario is one compiled pickle of an AST node: the executable name
// (outline rows interpolated) and the steps it substituted.
type pickleScenario struct {
	name  string
	steps []scenarioStep
}

// indexedSteps projects a pickle's steps onto the shape a vocabulary policy
// reads: the line plus its argument blocks.
func indexedSteps(steps []*messages.PickleStep) []scenarioStep {
	indexed := make([]scenarioStep, 0, len(steps))
	for _, step := range steps {
		indexed = append(indexed, scenarioStep{Text: step.Text})
		argument := step.Argument
		if argument == nil {
			continue
		}
		if argument.DocString != nil {
			indexed[len(indexed)-1].DocString = argument.DocString.Content
		}
		if argument.DataTable != nil {
			cells := make([]string, 0, len(argument.DataTable.Rows))
			for _, row := range argument.DataTable.Rows {
				for _, cell := range row.Cells {
					cells = append(cells, cell.Value)
				}
			}
			indexed[len(indexed)-1].Cells = cells
		}
	}
	return indexed
}

// appendUniqueSteps adds src's steps to dst, skipping a step dst already holds:
// repeated outline rows whose substitution is identical are one statement.
func appendUniqueSteps(dst, src []scenarioStep) []scenarioStep {
	seen := make(map[string]bool, len(dst))
	for _, step := range dst {
		seen[step.signature()] = true
	}
	for _, step := range src {
		if seen[step.signature()] {
			continue
		}
		seen[step.signature()] = true
		dst = append(dst, step)
	}
	return dst
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
