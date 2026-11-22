package main

// prove.go implements the `spec prove` verification gate. It is deliberately
// separate from `spec check`: check is static and part of every audit, prove is
// slow, needs Postgres, and is run by a human (or a nightly job) as the final
// answer to "the rule's enforcement still refuses the bypass".
//
// The registry docs/spec/prove.yaml maps a rule to the executable scenarios
// that detect its bypass. Prove derives the rules a change touches (or takes a
// manual --rule), validates the registry statically, then runs only the mapped
// godog scenarios. A rule with no registry entry is UNVALIDATABLE and fails
// unless the registry carries a git-attributed signoff.
//
// The base ref defaults to the repository's default branch: origin/HEAD,
// then origin/main, origin/master, main, master. The diff includes committed
// changes (base...HEAD) and the dirty tree (unstaged, staged and untracked
// files), so a proof is never skipped because the work is not committed yet.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	proveRegistryFile = "docs/spec/prove.yaml"
	proveDSNEnv       = "LT_API_TEST_DSN"
	proveFeaturePaths = "SPEC_FEATURE_PATHS"
)

// proveEntry is one registry row. Scenarios are `feature::scenario` pointers;
// Signoff is the human exception for a rule with no executable mutant.
type proveEntry struct {
	Scenarios []string `yaml:"scenarios"`
	Signoff   string   `yaml:"signoff"`
	Notes     string   `yaml:"notes"`
}

type proveRegistry map[string]proveEntry

func runProve(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec prove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules", "path to the per-rule-file catalog directory")
	registryPath := fs.String("registry", proveRegistryFile, "path to the per-rule mutant registry")
	base := fs.String("base", "", "git ref to diff against (default: the repository's default branch)")
	ruleID := fs.String("rule", "", "prove one rule id, bypassing the diff-derived scope")
	all := fs.Bool("all", false, "prove every rule in the registry (the seeded-registry gate)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "spec prove: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	catalog, err := loadCatalog(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec prove: %v\n", err)
		return 1
	}
	root, err := repoRoot(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec prove: %v\n", err)
		return 1
	}

	registry, err := loadProveRegistry(filepath.Join(root, filepath.FromSlash(*registryPath)))
	if err != nil {
		fmt.Fprintf(stderr, "spec prove: %v\n", err)
		return 1
	}

	var scope Scope
	baseLabel := ""
	switch {
	case *all:
		ids := make([]string, 0, len(registry))
		for id := range registry {
			ids = append(ids, id)
		}
		scope = Scope{Rules: ids}
		baseLabel = "registry (--all)"
	case *ruleID != "":
		scope, err = ParseCLI("", *ruleID)
		if err != nil {
			fmt.Fprintf(stderr, "spec prove: %v\n", err)
			return 2
		}
		if err := scope.Resolve(catalog, displayPath(*rulesPath)); err != nil {
			fmt.Fprintf(stderr, "spec prove: %v\n", err)
			return 1
		}
		baseLabel = "manual --rule"
	default:
		if *base == "" {
			*base, err = defaultProveBase(root)
			if err != nil {
				fmt.Fprintf(stderr, "spec prove: %v\n", err)
				return 1
			}
		}
		index := (&checker{root: root}).indexFeatures()
		derived, err := deriveProveScope(root, *base, *rulesPath, registryRelPath(root, *registryPath), catalog, index)
		if err != nil {
			fmt.Fprintf(stderr, "spec prove: %v\n", err)
			return 1
		}
		ids := make([]string, 0, len(derived))
		for id := range derived {
			ids = append(ids, id)
		}
		scope = Scope{Rules: ids}
		baseLabel = *base + "...HEAD + dirty tree"
	}

	if scope.Empty() {
		fmt.Fprintln(stdout, "spec prove: nothing to prove (no touched rule in the diff and no --rule)")
		return 0
	}

	if strings.TrimSpace(os.Getenv(proveDSNEnv)) == "" {
		fmt.Fprintf(stderr,
			"spec prove: %s is not set — prove is a database gate and fails closed.\n"+
				"bring the compose database up (`make db/up`) and export %s.\n"+
				"AUDIT_SKIP_DB does not apply to prove: a proof run that tested nothing is a failure.\n",
			proveDSNEnv, proveDSNEnv)
		return 1
	}

	index := (&checker{root: root}).indexFeatures()
	scenarioByPointer := map[string][]oracleScenario{}
	for _, scenario := range (&checker{root: root}).parseOracleScenarios(index) {
		scenarioByPointer[scenario.ref.pointer()] = append(scenarioByPointer[scenario.ref.pointer()], scenario)
	}

	ids := scope.IDs()

	var verified, unvalidatable, brokenTest, vacuous, signedOff, hardFailures int
	fmt.Fprintf(stdout, "spec prove: scope %s — %d rule(s)\n\n", baseLabel, len(ids))
	for _, id := range ids {
		entry, registered := registry[id]
		rule := ruleByID(catalog, id)

		if !registered || (len(entry.Scenarios) == 0 && strings.TrimSpace(entry.Signoff) == "") {
			unvalidatable++
			if strings.TrimSpace(entry.Signoff) != "" {
				signedOff++
				fmt.Fprintf(stdout, "UNVALIDATABLE %-15s no executable scenario; signoff: %s\n", id, entry.Signoff)
				continue
			}
			hardFailures++
			fmt.Fprintf(stdout, "UNVALIDATABLE %-15s no registry entry; map a mutant scenario or add a git-attributed signoff\n", id)
			continue
		}
		if len(entry.Scenarios) == 0 {
			signedOff++
			fmt.Fprintf(stdout, "UNVALIDATABLE %-15s no executable scenario; signoff: %s\n", id, entry.Signoff)
			continue
		}

		var reason string
		for _, pointer := range entry.Scenarios {
			if _, ok := index.byPointer[pointer]; !ok {
				reason = fmt.Sprintf("registry scenario %q does not exist in the parsed feature tree", pointer)
				break
			}
		}
		if reason == "" {
			reason = proveVacuity(rule, entry.Scenarios, scenarioByPointer)
		}
		if reason != "" {
			vacuous++
			hardFailures++
			fmt.Fprintf(stdout, "VACUOUS       %-15s %s\n", id, reason)
			continue
		}

		code, output, err := runProveScenarios(root, entry.Scenarios, index)
		if err != nil && code == 0 {
			brokenTest++
			hardFailures++
			fmt.Fprintf(stdout, "BROKEN-TEST   %-15s could not start the godog run: %v\n", id, err)
			continue
		}
		if code != 0 {
			brokenTest++
			hardFailures++
			fmt.Fprintf(stdout, "BROKEN-TEST   %-15s %d scenario(s) failed while the codebase is intact\n", id, len(entry.Scenarios))
			printTail(stdout, output, 8)
			continue
		}
		verified++
		fmt.Fprintf(stdout, "VERIFIED      %-15s %d scenario(s) refused the bypass\n", id, len(entry.Scenarios))
	}

	fmt.Fprintf(stdout,
		"\nspec prove: %d verified, %d unvalidatable (%d signed off), %d broken-test, %d vacuous — %d hard failure(s)\n",
		verified, unvalidatable, signedOff, brokenTest, vacuous, hardFailures)
	if hardFailures > 0 {
		return 1
	}
	return 0
}

// registryRelPath normalizes the registry flag to a repository-relative path.
// A relative flag is already root-relative (the same convention as --rules).
func registryRelPath(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func ruleByID(catalog Catalog, id string) Rule {
	for _, rule := range catalog {
		if rule.ID == id {
			return rule
		}
	}
	return Rule{ID: id}
}

// proveVacuity is the static half of a prove verdict. A scenario that asserts
// nothing (every Then is vacuous, or there is no Then at all) cannot detect a
// mutant; neither can a write-allowed scenario for a rule that claims the
// database refuses the same write.
func proveVacuity(rule Rule, pointers []string, scenarios map[string][]oracleScenario) string {
	for _, pointer := range pointers {
		rows := scenarios[pointer]
		if len(rows) == 0 {
			return fmt.Sprintf("scenario %q has no parsed steps", pointer)
		}
		for _, row := range rows {
			thens := thenSteps(row.steps)
			if len(thens) == 0 {
				return fmt.Sprintf("scenario %q has no Then outcome", pointer)
			}
			nonVacuous := false
			for _, step := range thens {
				if vacuousReason(step.Text) == "" {
					nonVacuous = true
				}
				if claimsDatabaseRefusal(rule) && isWriteAllowed(step.Text) {
					return fmt.Sprintf("scenario %q asserts that the write is allowed, but the rule claims database refusal", pointer)
				}
			}
			if !nonVacuous {
				return fmt.Sprintf("scenario %q statically asserts nothing observable", pointer)
			}
		}
	}
	return ""
}

func claimsDatabaseRefusal(rule Rule) bool {
	return rule.Authority == "database" && rule.Refusal != nil
}

func isWriteAllowed(text string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "the write is allowed")
}

// runProveScenarios runs only the mapped scenarios. Godog addresses a scenario
// by `path:line`, and an outline's row shares the scenario's declaration line,
// so one pointer runs all of its examples. The test harness reads
// SPEC_FEATURE_PATHS to replace its usual whole-tree walk.
func runProveScenarios(root string, pointers []string, index *featureIndex) (int, string, error) {
	seen := map[string]bool{}
	var paths []string
	for _, pointer := range pointers {
		ref, ok := index.byPointer[pointer]
		if !ok {
			continue
		}
		// godog resolves paths from the package directory the suite runs in
		// (internal/spec), not from the repository root.
		path := filepath.ToSlash(filepath.Join("..", "..", ref.Path)) + ":" + strconv.Itoa(ref.Line)
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	cmd := exec.Command("go", "test", "./internal/spec", "-count=1", "-run", "^$")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), proveFeaturePaths+"="+strings.Join(paths, "\n"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output), nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output), nil
	}
	return 1, string(output), err
}

func printTail(w io.Writer, output string, lines int) {
	parts := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	for _, line := range parts {
		fmt.Fprintf(w, "    %s\n", line)
	}
}

// loadProveRegistry reads the per-rule mutant registry. Decoding is strict — an
// unknown field is an error (a misspelled `scenarios` would otherwise register a
// rule as UNVALIDATABLE without saying so) — and the registry is exactly one
// YAML document, for the same reason the catalog is.
func loadProveRegistry(path string) (proveRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prove registry: %w", err)
	}
	var registry proveRegistry
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&registry); err != nil {
		return nil, fmt.Errorf("parse prove registry %s: %w", path, err)
	}
	// Keep decoding until EOF: a second document is a merge accident, not a
	// registry.
	trailing := fmt.Errorf("parse prove registry %s: trailing YAML document after the first — "+
		"the registry must be exactly one document", path)
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		// Exactly one document: the registry.
	case err != nil:
		return nil, fmt.Errorf("%w: %w", trailing, err)
	default:
		return nil, trailing
	}
	return registry, nil
}

// defaultProveBase finds the repository's default branch, trying the remote
// HEAD first and then the conventional branch names.
func defaultProveBase(root string) (string, error) {
	if out, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if base := strings.TrimSpace(out); base != "" {
			return base, nil
		}
	}
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := gitOutput(root, "rev-parse", "--verify", "--quiet", candidate+"^{commit}"); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot detect the default branch; pass --base <git-ref>")
}

// deriveProveScope maps the touched files onto the rules they name. A touched
// Go file contributes its //rule annotations, a touched per-rule file under the
// catalog directory contributes that rule when its file changed (each file is
// one rule), and a touched feature file contributes every rule it tags.
func deriveProveScope(root, base, rulesPath, registryPath string, catalog Catalog, index *featureIndex) (map[string]bool, error) {
	touched, err := touchedFiles(root, base)
	if err != nil {
		return nil, err
	}
	scope := map[string]bool{}
	relRules, _ := filepath.Rel(root, rulesPath)
	relRules = filepath.ToSlash(relRules)
	rulesPrefix := strings.TrimSuffix(relRules, "/") + "/"
	fileRules := map[string][]string{}
	for _, file := range index.files {
		fileRules[file.Path] = file.Rules
	}

	// The catalog is one file per rule, so the touched-set comparison runs once
	// over every touched file under the rules directory.
	var touchedRules []string
	for _, file := range touched {
		if strings.HasPrefix(file, rulesPrefix) {
			touchedRules = append(touchedRules, file)
		}
	}
	if len(touchedRules) > 0 {
		changed, err := changedRuleIDs(root, base, relRules, catalog, touchedRules)
		if err != nil {
			return nil, err
		}
		for _, id := range changed {
			scope[id] = true
		}
	}

	for _, file := range touched {
		switch {
		case strings.HasSuffix(file, ".go"):
			data, err := readWorkingOrBase(root, base, file)
			if err != nil {
				continue
			}
			for _, id := range goAnnotationIDs(file, data) {
				scope[id] = true
			}
		case strings.HasSuffix(file, ".feature"):
			for _, id := range fileRules[file] {
				scope[id] = true
			}
		case file == registryPath:
			// Editing the registry is itself a claim about these rules: prove
			// every entry the edited file declares.
			registry, err := loadProveRegistry(filepath.Join(root, filepath.FromSlash(file)))
			if err != nil {
				return nil, err
			}
			for id := range registry {
				scope[id] = true
			}
		}
	}
	return scope, nil
}

// goAnnotationIDs reads the //rule: annotations from parsed Go comments, not
// from raw text: a `//rule:` inside a string literal (a test fixture, a doc
// example) is not an annotation and must not widen prove's scope.
func goAnnotationIDs(filename string, data []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var ids []string
	for _, group := range file.Comments {
		for _, id := range annotationIDsOf(group) {
			if idPattern.MatchString(id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// changedRuleIDs compares the touched per-rule files with their base revision.
// The catalog is one file per rule, so every file under the rules directory is
// exactly one rule and the file's stem is its id:
//
//   - the base tree has no file there: the rule is ADDED;
//   - the working tree no longer has the file: the rule is REMOVED;
//   - both sides exist: the two are parsed with the live strict rule-file parser
//     and a deep-equality difference makes the rule CHANGED.
//
// A file that is identical on both sides contributes nothing. When the base tree
// has no rules directory at all (the catalog is wholly new), every rule of the
// current catalog counts as added.
func changedRuleIDs(root, base, relRules string, current Catalog, touched []string) ([]string, error) {
	if _, err := gitOutput(root, "cat-file", "-e", base+":"+relRules); err != nil {
		// A catalog directory that did not exist on the base is wholly new.
		ids := make([]string, 0, len(current))
		for _, rule := range current {
			ids = append(ids, rule.ID)
		}
		sort.Strings(ids)
		return ids, nil
	}

	changed := map[string]bool{}
	for _, file := range touched {
		name := filepath.Base(filepath.FromSlash(file))
		// The rules directory's authored README is not a rule, and the loader
		// would reject anything else that is not <module>/<ID>.md.
		if name == catalogReadmeFile || filepath.Ext(name) != ".md" {
			continue
		}
		id := strings.TrimSuffix(name, filepath.Ext(name))

		out, baseErr := gitOutput(root, "show", base+":"+file)
		working, workErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		switch {
		case baseErr != nil && workErr != nil:
			// Neither side has the file: nothing changed.
			continue
		case baseErr != nil:
			changed[id] = true // ADDED
		case workErr != nil:
			changed[id] = true // REMOVED
		default:
			before, err := parseRuleFile([]byte(out), base+":"+file)
			if err != nil {
				return nil, fmt.Errorf("parse base rule file %s:%s: %w", base, file, err)
			}
			after, err := parseRuleFile(working, file)
			if err != nil {
				return nil, fmt.Errorf("parse rule file %s: %w", file, err)
			}
			if !reflect.DeepEqual(before, after) {
				changed[id] = true // CHANGED
			}
		}
	}

	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// touchedFiles is the union of committed changes since base and the dirty
// working tree (unstaged, staged and untracked files).
func touchedFiles(root, base string) ([]string, error) {
	var files []string
	collect := func(args ...string) error {
		out, err := gitOutput(root, args...)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(out, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, filepath.ToSlash(line))
			}
		}
		return nil
	}
	if err := collect("diff", "--name-only", base+"...HEAD"); err != nil {
		return nil, fmt.Errorf("diff against %s: %w", base, err)
	}
	if err := collect("diff", "--name-only"); err != nil {
		return nil, err
	}
	if err := collect("diff", "--name-only", "--cached"); err != nil {
		return nil, err
	}
	if err := collect("ls-files", "--others", "--exclude-standard"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(files))
	for _, file := range files {
		if !seen[file] {
			seen[file] = true
			unique = append(unique, file)
		}
	}
	sort.Strings(unique)
	return unique, nil
}

func readWorkingOrBase(root, base, rel string) ([]byte, error) {
	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		return data, nil
	}
	out, err := gitOutput(root, "show", base+":"+rel)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// gitOutput runs git in root and returns its standard output. It is a variable
// so a test can stub the git seam — the per-file comparison needs a base tree
// the test controls — while production code always uses the real command.
var gitOutput = func(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg != "" {
				return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
			}
		}
		return "", err
	}
	return string(out), nil
}
