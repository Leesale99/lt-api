package main

// oracle.go implements checker policy 18 (`18-oracle`,
// docs/spec/rules.schema.md §3): a scenario must assert something an observer
// can see, and the assertion must match the rule's category.
//
//   - VACUOUS THEN. A Then step (and every And/But continuation of one) that
//     only says the code did not explode — "runs without throwing", "does not
//     panic", "completes successfully", "the call succeeds", "no error occurs"
//     — is an ERROR. It would pass after the rule was deleted.
//   - CATEGORY BINDING. Policy 14's binding is reused here: an `interface` rule
//     must have at least one scenario asserting a refusal (a refused-write step,
//     or a name from the rule's refusal block); an `invariant` or `policy` rule
//     must have at least one scenario with a non-vacuous Then outcome. All
//     other categories bind nothing and are exempt.
//   - RE-DERIVED EXPECTATIONS. A Then step's quoted literal, decimal, or
//     integer above the scenario-coordinate range (1–38) that already appears
//     verbatim in executable Go source at the rule's annotated enforcement site
//     is an ERROR: the expected value was copied from the implementation, so
//     the scenario cannot disagree with it. Constants and variables are
//     exempt — domain vocabulary necessarily lives there — and only Go sites
//     (never migrations or tests) contribute literals.
//
// The collector walks the parsed feature tree exactly as policy 13 does — the
// shared featureIndex (features.go), never a second parse — so a finding names
// the same `file::scenario` pointer a catalog verification entry uses. Rules
// with no executable scenario are outside its reach (policy 14 already reports
// their missing proof).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	messages "github.com/cucumber/messages/go/v34"
)

const (
	// policyOracle is the policy id every scenario-observability finding is
	// reported under.
	policyOracle = "18-oracle"

	// oracleScenarioCoordinateMax is the largest bare integer treated as a
	// scenario coordinate (a match day or round number) rather than an expected
	// value. The round vocabulary is 1–38, so a bare figure in that range is
	// overwhelmingly a fixture address ("match day 1"), not an asserted value.
	oracleScenarioCoordinateMax = 38
)

// vacuousThenPatterns are the unconditional "nothing went wrong" shapes. The
// list is deliberately small and phrase-shaped: it names the vacuous wording,
// not every Then step that happens to mention success.
var vacuousThenPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`\b(?:runs?|executes?|works?|passes?)\s+without\s+(?:throwing|panicking|error|errors|failing|failures?)\b`), "it only says the code did not throw"},
	{regexp.MustCompile(`\b(?:does|do|did)\s+not\s+(?:throw|panic|error|fail|explode)\b`), "it only says the code did not fail"},
	{regexp.MustCompile(`\b(?:no|zero)\s+errors?\s+(?:occur|occurs|occurred|is\s+raised|are\s+raised|happens?)\b`), "it only says no error occurred"},
	{regexp.MustCompile(`\b(?:completes?|finishes?|succeeds?)\s+(?:successfully|without\s+error|cleanly|normally)\b`), "it only says the call completed"},
	{regexp.MustCompile(`\bthe\s+call\s+(?:succeeds|works|returns|completes)\b`), "it only says the call succeeded"},
	{regexp.MustCompile(`\b(?:nothing|no\s+(?:error|exception|panic))\s+(?:happens?|is\s+thrown|is\s+raised)\b`), "it only says nothing happened"},
}

var (
	oracleNumberTokenPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	oracleFloatPattern       = regexp.MustCompile(`[0-9]+\.[0-9]+`)
)

// oracleScenario is one executable scenario plus the rule its tags name.
type oracleScenario struct {
	ref   scenarioRef
	steps []scenarioStep
}

// thenSteps returns the outcome steps of a scenario. gherkin's pickle compiler
// gives an And/But continuation the previous step's type, so a continuation of
// a Then is an Outcome too.
func thenSteps(steps []scenarioStep) []scenarioStep {
	out := make([]scenarioStep, 0, len(steps))
	for _, step := range steps {
		if step.Type == messages.PickleStepType_OUTCOME {
			out = append(out, step)
		}
	}
	return out
}

// vacuousReason returns why a Then step asserts nothing observable, or "".
func vacuousReason(text string) string {
	lowered := strings.ToLower(strings.TrimSpace(text))
	for _, pattern := range vacuousThenPatterns {
		if pattern.re.MatchString(lowered) {
			return pattern.reason
		}
	}
	return ""
}

// categoryBinding is policy 14's binding in the form policy 18 consumes.
func categoryBinding(category string) string {
	switch category {
	case "interface":
		return "refusal"
	case "invariant", "policy":
		return "state"
	}
	return ""
}

// assertsRefusal reports whether a Then step names a refusal an observer can
// see: the refused-write family, refusal vocabulary, or one of the rule's own
// refusal constraint/sentinel names.
func assertsRefusal(rule Rule, text string) bool {
	lowered := strings.ToLower(text)
	if strings.Contains(lowered, "refus") ||
		strings.Contains(lowered, "reject") ||
		strings.Contains(lowered, "denied") ||
		strings.Contains(lowered, "not allowed") ||
		strings.HasPrefix(lowered, "the write is refused") {
		return true
	}
	if rule.Refusal == nil {
		return false
	}
	if rule.Refusal.Code != "" && strings.Contains(text, rule.Refusal.Code) {
		return true
	}
	for _, name := range rule.Refusal.Constraint {
		if name != "" && strings.Contains(text, name) {
			return true
		}
	}
	if rule.Refusal.Sentinel != nil && *rule.Refusal.Sentinel != "" &&
		strings.Contains(text, *rule.Refusal.Sentinel) {
		return true
	}
	return false
}

// collectOracle runs policy 18 over the feature tree. It reads the one parse the
// policy environment holds, so the check run parses the feature files once.
func collectOracle(env *policyEnv) []Finding {
	c := newChecker(env)
	scenarios := oracleScenarios(env.featureIndex())

	c.checkVacuousThen(scenarios)
	c.checkCategoryAssertions(env.catalog, scenarios)
	c.checkReDerivedExpectations(env.catalog, scenarios)
	sortFindings(c.findings)
	return c.findings
}

// oracleScenarios projects the shared feature index onto the shape policy 18
// reads: every scenario that names a rule, with the steps the index already
// retained (text, pickle step type, docstring, table cells). Rule attribution
// comes from the same featureIndex policy 12 builds, so a scenario under a
// tagged Rule: group is attributed identically.
func oracleScenarios(index *featureIndex) []oracleScenario {
	scenarios := make([]oracleScenario, 0, len(index.scenarios))
	for _, ref := range index.scenarios {
		if ref.Rule == "" {
			continue
		}
		scenarios = append(scenarios, oracleScenario{ref: ref, steps: ref.Steps})
	}
	return scenarios
}

// checkVacuousThen is policy 18(a): no Then step may only say the code did not
// explode. A Scenario Outline's rows share a pointer, so the finding is
// deduplicated per scenario pointer and message.
func (c *checker) checkVacuousThen(scenarios []oracleScenario) {
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		for _, step := range thenSteps(scenario.steps) {
			reason := vacuousReason(step.Text)
			if reason == "" {
				continue
			}
			key := scenario.ref.pointer() + "\x00" + step.Text
			if seen[key] {
				continue
			}
			seen[key] = true
			c.errorf(policyOracle, scenario.ref.Rule,
				"scenario %q has a vacuous Then step %q: %s; assert a concrete state, side effect or refusal instead",
				scenario.ref.pointer(), step.Text, reason)
		}
	}
}

// checkCategoryAssertions is policy 18(b): the rule's category says what its
// proof must observe, and at least one of its scenarios must observe it.
func (c *checker) checkCategoryAssertions(catalog Catalog, scenarios []oracleScenario) {
	byRule := map[string][]oracleScenario{}
	pointers := map[string]map[string]bool{}
	for _, scenario := range scenarios {
		byRule[scenario.ref.Rule] = append(byRule[scenario.ref.Rule], scenario)
		if pointers[scenario.ref.Rule] == nil {
			pointers[scenario.ref.Rule] = map[string]bool{}
		}
		pointers[scenario.ref.Rule][scenario.ref.pointer()] = true
	}

	for _, rule := range catalog {
		binding := categoryBinding(rule.Category)
		if binding == "" || len(byRule[rule.ID]) == 0 {
			continue
		}
		satisfied := false
		for _, scenario := range byRule[rule.ID] {
			for _, step := range thenSteps(scenario.steps) {
				if binding == "refusal" {
					if assertsRefusal(rule, step.Text) {
						satisfied = true
					}
					continue
				}
				if vacuousReason(step.Text) == "" {
					satisfied = true
				}
			}
		}
		if satisfied {
			continue
		}
		count := len(pointers[rule.ID])
		if binding == "refusal" {
			c.errorf(policyOracle, rule.ID,
				"rule category %q binds refusal, but none of its %d scenario(s) asserts a refusal observable (a refused-write step, refusal code, constraint, or sentinel name)",
				rule.Category, count)
			continue
		}
		c.errorf(policyOracle, rule.ID,
			"rule category %q binds state or side effect, but none of its %d scenario(s) has a non-vacuous Then outcome",
			rule.Category, count)
	}
}

// checkReDerivedExpectations is policy 18(c): an expected literal copied
// verbatim from the annotated Go enforcement source cannot disagree with the
// implementation. The detector is intentionally narrow: only executable
// literals count (const/var/type declarations are domain vocabulary), and a
// bare integer in the scenario-coordinate range 1–38 is ignored.
func (c *checker) checkReDerivedExpectations(catalog Catalog, scenarios []oracleScenario) {
	byRule := map[string][]oracleScenario{}
	for _, scenario := range scenarios {
		byRule[scenario.ref.Rule] = append(byRule[scenario.ref.Rule], scenario)
	}

	seen := map[string]bool{}
	for _, rule := range catalog {
		literals := c.executableSiteLiterals(rule)
		if len(literals) == 0 {
			continue
		}
		for _, scenario := range byRule[rule.ID] {
			for _, step := range thenSteps(scenario.steps) {
				for _, literal := range expectedLiterals(step.Text) {
					if !literals[literal] {
						continue
					}
					key := scenario.ref.pointer() + "\x00" + literal
					if seen[key] {
						continue
					}
					seen[key] = true
					c.errorf(policyOracle, rule.ID,
						"scenario %q asserts %q, which already appears verbatim in the rule's executable enforcement source; the expected value is re-derived from the implementation instead of the business rule",
						scenario.ref.pointer(), literal)
				}
			}
		}
	}
}

// expectedLiterals returns the quoted strings and numbers a Then step asserts.
// Quoted strings are taken from the original text; numbers are read from the
// unquoted token stream so a figure inside a quoted value is not counted twice.
func expectedLiterals(text string) []string {
	var literals []string
	for _, quoted := range quotedLiteralPattern.FindAllString(text, -1) {
		literal := strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`)
		if literal != "" {
			literals = append(literals, literal)
		}
	}
	for _, token := range tokenizeStep(text) {
		if !oracleNumberTokenPattern.MatchString(token) {
			continue
		}
		if !oracleFloatPattern.MatchString(token) {
			if value, err := strconv.Atoi(token); err == nil &&
				value >= 1 && value <= oracleScenarioCoordinateMax {
				continue
			}
		}
		literals = append(literals, token)
	}
	return literals
}

// executableSiteLiterals collects the string, integer and float literals in
// executable code at a rule's Go enforcement sites. GenDecl nodes (const, var,
// type) are skipped whole: a state name such as `"locked"` lives there because
// it is domain vocabulary, not because the scenario copied an implementation
// detail.
func (c *checker) executableSiteLiterals(rule Rule) map[string]bool {
	set := map[string]bool{}
	cache := map[string]map[string]bool{}
	for _, site := range rule.Enforcement {
		req, ok := annotationRequirement(rule.ID, site.Site)
		if !ok {
			continue
		}
		literals, seen := cache[req.path]
		if !seen {
			literals = c.readExecutableLiterals(req.path)
			cache[req.path] = literals
		}
		for literal := range literals {
			set[literal] = true
		}
	}
	return set
}

func (c *checker) readExecutableLiterals(rel string) map[string]bool {
	set := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		return set
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, parser.SkipObjectResolution)
	if err != nil {
		return set
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.GenDecl:
			return false
		case *ast.BasicLit:
			switch n.Kind {
			case token.STRING:
				if value, err := strconv.Unquote(n.Value); err == nil {
					set[value] = true
				}
			case token.INT, token.FLOAT:
				set[n.Value] = true
			}
		}
		return true
	})
	return set
}
