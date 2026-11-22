package main

// Tests for `spec prove`: the registry seeds the 30 database-authority rules,
// every mapped scenario resolves, the static vacuity verdict is the same rule
// policy 18 applies, and the DB gate fails closed. The scope-derivation tests
// exercise the per-rule-file catalog: a touched file is one rule (added,
// removed or changed), and the git seam is stubbed where a base revision would
// otherwise need a real commit.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	messages "github.com/cucumber/messages/go/v34"
)

func realCatalogAndRoot(t *testing.T) (Catalog, string) {
	t.Helper()
	root := filepath.Join("..", "..")
	catalog, err := loadCatalog(filepath.Join(root, "docs", "spec", "rules"))
	if err != nil {
		t.Fatalf("load the real catalog: %v", err)
	}
	return catalog, root
}

func TestProveRegistrySeedsTheThirtyRules(t *testing.T) {
	catalog, root := realCatalogAndRoot(t)
	registry, err := loadProveRegistry(filepath.Join(root, proveRegistryFile))
	if err != nil {
		t.Fatalf("load prove registry: %v", err)
	}

	want := []string{
		"R-SCHEDULE-003", "R-SCHEDULE-004", "R-SCHEDULE-005", "R-SCHEDULE-006",
		"R-SCHEDULE-007", "R-SCHEDULE-008", "R-SCHEDULE-009", "R-SCHEDULE-010",
		"R-SCHEDULE-011", "R-SCHEDULE-013", "R-SCHEDULE-014", "R-SCHEDULE-015",
		"R-SCHEDULE-016", "R-SCHEDULE-023", "R-SCHEDULE-024", "R-SCHEDULE-025",
		"R-SCHEDULE-029", "R-GAME-001", "R-GAME-002", "R-GAME-003", "R-GAME-004",
		"R-GAME-005", "R-GAME-014", "R-GAME-025", "R-GAME-035", "R-IDENTITY-006",
		"R-IDENTITY-011", "R-IDENTITY-013", "R-IDENTITY-014", "R-IDENTITY-020",
	}
	if len(registry) != len(want) {
		t.Fatalf("registry has %d entries, want %d", len(registry), len(want))
	}
	for _, id := range want {
		if _, ok := registry[id]; !ok {
			t.Errorf("registry has no entry for %s", id)
		}
	}

	index := (&checker{root: root}).indexFeatures()
	known := map[string]bool{}
	for _, rule := range catalog {
		known[rule.ID] = true
	}
	for id, entry := range registry {
		if !known[id] {
			t.Errorf("registry names unknown rule %s", id)
			continue
		}
		if len(entry.Scenarios) == 0 {
			t.Errorf("registry entry %s has no scenario (seed entries are verifiable)", id)
		}
		for _, pointer := range entry.Scenarios {
			ref, ok := index.byPointer[pointer]
			if !ok {
				t.Errorf("%s maps to a scenario that does not exist: %s", id, pointer)
				continue
			}
			if ref.Rule != id {
				t.Errorf("%s maps to %s, which is tagged %s", id, pointer, ref.Rule)
			}
		}
	}
}

func outcomeStep(text string) scenarioStep {
	return scenarioStep{Text: text, Type: messages.PickleStepType_OUTCOME}
}

func TestProveVacuityMatchesPolicy18(t *testing.T) {
	rule := baseRule()
	rule.Authority = "database"
	rule.Refusal = &Refusal{Code: "23505"}

	tests := []struct {
		name   string
		rule   Rule
		steps  []scenarioStep
		reason string
	}{
		{
			name:  "observable refusal",
			rule:  rule,
			steps: []scenarioStep{outcomeStep("the write is refused by things_check")},
		},
		{
			name:   "no Then at all",
			rule:   rule,
			steps:  []scenarioStep{{Text: "the thing is written", Type: messages.PickleStepType_ACTION}},
			reason: "no Then outcome",
		},
		{
			name:   "vacuous Then",
			rule:   rule,
			steps:  []scenarioStep{outcomeStep("the call succeeds")},
			reason: "asserts nothing observable",
		},
		{
			name:   "write allowed on a database-refusal rule",
			rule:   rule,
			steps:  []scenarioStep{outcomeStep("the write is allowed")},
			reason: "claims database refusal",
		},
		{
			name: "write allowed on a rule that claims no refusal",
			rule: func() Rule {
				r := rule
				r.Refusal = nil
				return r
			}(),
			steps: []scenarioStep{outcomeStep("the write is allowed")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenarios := map[string][]oracleScenario{
				"f.feature::s": {{ref: scenarioRef{Path: "f.feature", Name: "s", Rule: tt.rule.ID}, steps: tt.steps}},
			}
			reason := proveVacuity(tt.rule, []string{"f.feature::s"}, scenarios)
			if tt.reason == "" {
				if reason != "" {
					t.Errorf("unexpected VACUOUS verdict: %s", reason)
				}
				return
			}
			if !strings.Contains(reason, tt.reason) {
				t.Errorf("verdict = %q, want it to mention %q", reason, tt.reason)
			}
		})
	}
}

// TestProveCountsOnlyAcceptedAnnotations pins the semantics prove inherits from
// the checker: prove derives scope from the same `//rule:` lexer policy 15 uses,
// so an annotation the checker would reject for placement or contiguity is not
// coverage. (Before the unification prove counted every comment group in the
// file, including a block detached from its declaration by a blank line.)
func TestProveCountsOnlyAcceptedAnnotations(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "contiguous doc block",
			source: "package p\n\n//rule:R-KERNEL-001\nfunc Do() {}\n",
			want:   []string{"R-KERNEL-001"},
		},
		{
			name:   "stacked block above one declaration",
			source: "package p\n\n//rule:R-KERNEL-001\n//rule:R-KERNEL-002\nfunc Do() {}\n",
			want:   []string{"R-KERNEL-001", "R-KERNEL-002"},
		},
		{
			name:   "trailing comment on the declaration line",
			source: "package p\n\nfunc Do() {} //rule:R-KERNEL-003\n",
			want:   []string{"R-KERNEL-003"},
		},
		{
			name:   "gofmt-normalized spelling is the same annotation",
			source: "package p\n\n// rule:R-KERNEL-004\nfunc Do() {}\n",
			want:   []string{"R-KERNEL-004"},
		},
		{
			name:   "block detached by a blank line is not this declaration's annotation",
			source: "package p\n\n//rule:R-KERNEL-005\n\nfunc Do() {}\n",
			want:   nil,
		},
		{
			name:   "a comment in the middle of a body names no declaration",
			source: "package p\n\nfunc Do() {\n\t//rule:R-KERNEL-006\n}\n",
			want:   nil,
		},
		{
			name:   "a rule: inside a string literal is not an annotation",
			source: "package p\n\nvar doc = \"//rule:R-KERNEL-007\"\n",
			want:   nil,
		},
		{
			name:   "a malformed id is not coverage",
			source: "package p\n\n//rule:nope\nfunc Do() {}\n",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := annotatedRuleIDs("thing.go", []byte(tt.source), annotationDeclared)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("annotatedRuleIDs(annotationDeclared) = %v, want %v", got, tt.want)
			}
		})
	}

	// The reverse direction still reads every comment group: a detached block's
	// ids must resolve, even though they are no declaration's annotation.
	detached := "package p\n\n//rule:R-KERNEL-005\n\nfunc Do() {}\n"
	if got := annotatedRuleIDs("thing.go", []byte(detached), annotationAnywhere); strings.Join(got, ",") != "R-KERNEL-005" {
		t.Errorf("annotatedRuleIDs(annotationAnywhere) = %v, want the detached id", got)
	}
}

func TestProveFailsClosedWithoutDatabase(t *testing.T) {
	t.Setenv(proveDSNEnv, "")
	var stdout, stderr bytes.Buffer
	code := runProve([]string{
		"--rules", filepath.Join("..", "..", "docs", "spec", "rules"),
		"--rule", "R-SCHEDULE-003",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("prove without a DSN = %d, want 1", code)
	}
	output := stdout.String() + stderr.String()
	for _, want := range []string{"fails closed", "AUDIT_SKIP_DB does not apply"} {
		if !strings.Contains(output, want) {
			t.Errorf("prove output does not mention %q:\n%s", want, output)
		}
	}
}

func TestProveRejectsUnknownRule(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProve([]string{
		"--rules", filepath.Join("..", "..", "docs", "spec", "rules"),
		"--rule", "R-NOPE-999",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("prove with an unknown rule = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown rule id") {
		t.Errorf("prove output does not report the unknown rule:\n%s", stderr.String())
	}
}

// proveRule builds a minimal rule that belongs to id's module and renders as a
// parseable rule file (the per-rule catalog's only representation).
func proveRule(id, module, entity, title string) Rule {
	rule := baseRule()
	rule.ID = id
	rule.Module = module
	rule.Entity = entity
	rule.Title = title
	return rule
}

func mustRenderRuleFile(t *testing.T, rule Rule) []byte {
	t.Helper()
	data, err := renderRuleFile(rule)
	if err != nil {
		t.Fatalf("render %s: %v", rule.ID, err)
	}
	return data
}

// stubGit replaces the git seam for one test: tree is the set of base revisions
// that exist (`<base>:<path>`, a file or a directory) and files maps
// `<base>:<path>` to that path's contents on the base. Anything else fails like
// a missing revision.
func stubGit(t *testing.T, tree map[string]bool, files map[string]string) {
	t.Helper()
	previous := gitOutput
	t.Cleanup(func() { gitOutput = previous })
	gitOutput = func(root string, args ...string) (string, error) {
		switch {
		case len(args) == 3 && args[0] == "cat-file" && args[1] == "-e":
			if tree[args[2]] {
				return "", nil
			}
			return "", fmt.Errorf("git cat-file -e %s: not found", args[2])
		case len(args) == 2 && args[0] == "show":
			if out, ok := files[args[1]]; ok {
				return out, nil
			}
			return "", fmt.Errorf("git show %s: not found", args[1])
		}
		return "", fmt.Errorf("stub git: unexpected git %v", args)
	}
}

func TestProveDerivesScopeFromDirtyTree(t *testing.T) {
	repo := newTestRepo(t)
	root := repo.root
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "spec@example.test")
	run("config", "user.name", "Spec Test")

	// The base catalog: a kernel rule that will change, one that stays clean, a
	// game rule that will be deleted, plus the directory's authored README.
	repo.writeCatalog(t, Catalog{
		proveRule("R-KERNEL-001", "kernel", "thing", "A thing always is"),
		proveRule("R-KERNEL-005", "kernel", "thing", "A thing stays as it is"),
		proveRule("R-GAME-001", "game", "ride", "A ride is born locked"),
	})
	repo.addFile(t, "docs/spec/rules/README.md", "# rules\n")
	repo.addFile(t, "docs/spec/features/kernel/thing.feature",
		"@kernel\nFeature: a thing\n\n  @rule:R-KERNEL-002\n  Scenario: a scenario\n    Then the thing reads \"locked\"\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")

	// Dirty the tree: a changed rule file, a new one, a deleted one, an edited
	// README (not a rule), a Go annotation and a retagged feature.
	repo.addFile(t, "docs/spec/rules/kernel/R-KERNEL-001.md",
		string(mustRenderRuleFile(t, proveRule("R-KERNEL-001", "kernel", "thing", "A thing always was"))))
	repo.addFile(t, "docs/spec/rules/kernel/R-KERNEL-006.md",
		string(mustRenderRuleFile(t, proveRule("R-KERNEL-006", "kernel", "thing", "A thing arrives"))))
	if err := os.Remove(filepath.Join(root, "docs", "spec", "rules", "game", "R-GAME-001.md")); err != nil {
		t.Fatal(err)
	}
	repo.addFile(t, "docs/spec/rules/README.md", "# rules, edited\n")
	repo.addFile(t, "internal/kernel/thing/thing.go", "package thing\n\n//rule:R-KERNEL-003\nfunc Do() {}\n")
	repo.addFile(t, "docs/spec/features/kernel/thing.feature",
		"@kernel\nFeature: a thing\n\n  @rule:R-KERNEL-004\n  Scenario: a scenario\n    Then the thing reads \"locked\"\n")

	catalog, err := loadCatalog(repo.rulesPath)
	if err != nil {
		t.Fatalf("load the working catalog: %v", err)
	}
	index := (&checker{root: root}).indexFeatures()
	scope, err := deriveProveScope(root, "HEAD", repo.rulesPath, "docs/spec/prove.yaml", catalog, index)
	if err != nil {
		t.Fatalf("derive scope: %v", err)
	}
	for _, want := range []string{
		"R-KERNEL-001", // CHANGED rule file
		"R-KERNEL-003", // //rule annotation in a touched Go file
		"R-KERNEL-004", // tag in a touched feature file
		"R-KERNEL-006", // ADDED rule file
		"R-GAME-001",   // REMOVED rule file
	} {
		if !scope[want] {
			t.Errorf("derived scope does not contain %s: %v", want, scope)
		}
	}
	for _, unwanted := range []string{
		"R-KERNEL-005", // the file did not change
		"README",       // the rules directory's README is not a rule
	} {
		if scope[unwanted] {
			t.Errorf("derived scope contains %s, which no touched rule file changed: %v", unwanted, scope)
		}
	}
}

func TestChangedRuleIDsComparesEveryTouchedRuleFile(t *testing.T) {
	root := t.TempDir()
	const rulesRel = "docs/spec/rules"
	write := func(rel string, data []byte) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	before := mustRenderRuleFile(t, proveRule("R-KERNEL-001", "kernel", "thing", "A thing always is"))
	after := mustRenderRuleFile(t, proveRule("R-KERNEL-001", "kernel", "thing", "A thing always was"))
	clean := mustRenderRuleFile(t, proveRule("R-KERNEL-005", "kernel", "thing", "A thing stays as it is"))
	added := mustRenderRuleFile(t, proveRule("R-KERNEL-006", "kernel", "thing", "A thing arrives"))
	deleted := mustRenderRuleFile(t, proveRule("R-GAME-001", "game", "ride", "A ride is born locked"))

	write(rulesRel+"/kernel/R-KERNEL-001.md", after)
	write(rulesRel+"/kernel/R-KERNEL-005.md", clean)
	write(rulesRel+"/kernel/R-KERNEL-006.md", added)
	// R-GAME-001 is absent from the working tree: it was removed.

	stubGit(t,
		map[string]bool{
			"HEAD:" + rulesRel:             true,
			"HEAD:" + rulesRel + "/kernel": true,
			"HEAD:" + rulesRel + "/game":   true,
		},
		map[string]string{
			"HEAD:" + rulesRel + "/kernel/R-KERNEL-001.md": string(before),
			"HEAD:" + rulesRel + "/kernel/R-KERNEL-005.md": string(clean),
			"HEAD:" + rulesRel + "/game/R-GAME-001.md":     string(deleted),
		},
	)

	current := Catalog{
		proveRule("R-KERNEL-001", "kernel", "thing", "A thing always was"),
		proveRule("R-KERNEL-005", "kernel", "thing", "A thing stays as it is"),
		proveRule("R-KERNEL-006", "kernel", "thing", "A thing arrives"),
	}
	touched := []string{
		rulesRel + "/kernel/R-KERNEL-001.md",
		rulesRel + "/kernel/R-KERNEL-005.md",
		rulesRel + "/kernel/R-KERNEL-006.md",
		rulesRel + "/game/R-GAME-001.md",
		rulesRel + "/README.md",
	}
	ids, err := changedRuleIDs(root, "HEAD", rulesRel, current, touched)
	if err != nil {
		t.Fatalf("changedRuleIDs: %v", err)
	}
	want := "R-GAME-001,R-KERNEL-001,R-KERNEL-006"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("changed ids = %s, want %s", got, want)
	}
}

func TestChangedRuleIDsTreatsAnAbsentBaseCatalogAsAllAdded(t *testing.T) {
	root := t.TempDir()
	const rulesRel = "docs/spec/rules"
	file := rulesRel + "/kernel/R-KERNEL-001.md"
	full := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, mustRenderRuleFile(t, proveRule("R-KERNEL-001", "kernel", "thing", "A thing always is")), 0o644); err != nil {
		t.Fatal(err)
	}

	// No rules directory on the base at all.
	stubGit(t, nil, nil)

	current := Catalog{
		proveRule("R-KERNEL-001", "kernel", "thing", "A thing always is"),
		proveRule("R-GAME-001", "game", "ride", "A ride is born locked"),
	}
	ids, err := changedRuleIDs(root, "HEAD", rulesRel, current, []string{file})
	if err != nil {
		t.Fatalf("changedRuleIDs: %v", err)
	}
	want := "R-GAME-001,R-KERNEL-001"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("changed ids = %s, want every current rule %s", got, want)
	}
}

func TestChangedRuleIDsRejectsAMalformedBaseRuleFile(t *testing.T) {
	root := t.TempDir()
	const rulesRel = "docs/spec/rules"
	file := rulesRel + "/kernel/R-KERNEL-001.md"
	full := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, mustRenderRuleFile(t, proveRule("R-KERNEL-001", "kernel", "thing", "A thing always is")), 0o644); err != nil {
		t.Fatal(err)
	}

	stubGit(t, map[string]bool{"HEAD:" + rulesRel: true}, map[string]string{
		"HEAD:" + file: "not a rule file\n",
	})

	_, err := changedRuleIDs(root, "HEAD", rulesRel, Catalog{baseRule()}, []string{file})
	if err == nil {
		t.Fatal("changedRuleIDs accepted a malformed base rule file")
	}
	if !strings.Contains(err.Error(), "parse base rule file HEAD:") {
		t.Errorf("error = %q, want the base-parse error style", err)
	}
}
