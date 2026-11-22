package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// genCatalog is deliberately shuffled: modules, entities and ids are not in
// file order. gen must sort them.
func genCatalog() Catalog {
	kernel := baseRule() // R-KERNEL-001
	kernel.Category = "policy"
	kernel.Authority = "domain"
	kernel.Notes = "Context nobody needs."
	kernel.StalePointer = ptr("TestDo moved in Phase B.")
	kernel.Refusal = &Refusal{Code: "synthetic", HTTP: ptr(422)}

	rideBorn := baseRule()
	rideBorn.ID = "R-GAME-001"
	rideBorn.Module = "game"
	rideBorn.Entity = "ride"
	rideBorn.Title = "Rides are born locked"
	rideBorn.Statement = "A ride is born locked; the client cannot choose another state."
	rideBorn.Category = "interface"
	rideBorn.Authority = "handler"
	rideBorn.Enforcement = []Enforcement{{
		Site: "internal/kernel/thing/thing.go::Do", Layer: "handler", Role: "authoritative",
	}}
	rideBorn.Notes = "Ordinary notes that should not reach the Markdown."
	rideBorn.KnowledgeRef = ptr("docs/league-tokens-learning-phases/phase-00-http-api-foundation.md")

	money := baseRule()
	money.ID = "R-GAME-002"
	money.Module = "game"
	money.Entity = "ride"
	money.Title = "Money and state move together"
	money.Statement = "A paid ride cannot lose its money without leaving its state."
	money.Category = "invariant"
	money.Authority = "database"
	money.Mechanism = ptr("constraint")
	money.WriteScope = ptr("row-local")
	money.Enforcement = []Enforcement{{
		Site:  "migrations/000001_create_initial_game_models.up.sql",
		Layer: "db-constraint", Role: "authoritative",
	}}
	money.Refusal = &Refusal{
		Code:       "23514",
		Constraint: StringList{"rides_money_state_coupling"},
		Sentinel:   ptr("ErrInvalidTransition"),
		HTTP:       ptr(409),
	}

	config := baseRule()
	config.ID = "R-PLATFORM-001"
	config.Module = "platform"
	config.Entity = "config"
	config.Title = "Configuration is parsed once"
	config.Statement = "Configuration is read once at start-up."
	config.Category = "orchestration"
	config.Authority = "service"
	config.Status = statusSpecifiedOnly
	config.Enforcement = nil
	config.Verification = Verification{}
	config.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
	config.Notes = "Specified only."

	return Catalog{config, rideBorn, kernel, money}
}

// genRepo is a synthetic checkout with the shuffled catalog already written.
func genRepo(t *testing.T) (*testRepo, string) {
	t.Helper()
	repo := newTestRepo(t)
	repo.writeCatalog(t, genCatalog())
	out := filepath.Join(repo.root, "docs", "spec", "business-rules.md")
	return repo, out
}

func readGenerated(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file %s: %v", path, err)
	}
	return string(data)
}

func TestGenDeterministic(t *testing.T) {
	repo, out := genRepo(t)

	var first string
	for run := 0; run < 2; run++ {
		var stdout, stderr bytes.Buffer
		if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
			t.Fatalf("runGen run %d = %d, stderr=%s", run, code, stderr.String())
		}
		if run == 0 {
			first = readGenerated(t, out)
		}
	}

	// Determinism: the second run is byte-identical.
	if got := readGenerated(t, out); got != first {
		t.Errorf("gen is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first, got)
	}

	dashboard := first
	assertContains := func(md, want string) {
		t.Helper()
		if !strings.Contains(md, want) {
			t.Errorf("generated dashboard is missing %q", want)
		}
	}

	// Header: generated marker, the authored per-rule files, regeneration, and
	// the never-hand-edit warning.
	assertContains(dashboard, "GENERATED — do not edit")
	assertContains(dashboard, "# Business rules — dashboard")
	assertContains(dashboard, "derived from the per-rule files")
	assertContains(dashboard, "under `"+displayPath(repo.rulesPath)+"`")
	assertContains(dashboard, "Never hand-edit the dashboard")
	assertContains(dashboard, "make spec/gen")
	assertContains(dashboard, "go run ./cmd/spec gen --rules "+displayPath(repo.rulesPath))

	// Dashboard: totals, counts, one link per module — no rule blocks.
	assertContains(dashboard, "**4 rules** across 3 modules — 3 implemented, 1 specified-only.")
	for _, want := range []string{
		"| `implemented` | 3 |", "| `specified-only` | 1 |",
		"| `game` | 2 |", "| `kernel` | 1 |", "| `platform` | 1 |",
		"| **total** | **4** |",
		"| `game` | 2 | [game](rules/game/) |",
		"| `ledger` | 0 | [ledger](rules/ledger/) |",
		"| **total** | **4** | |",
	} {
		assertContains(dashboard, want)
	}
	if strings.Contains(dashboard, "#### R-GAME-001") {
		t.Error("the dashboard carries rule blocks; the rule files are the only rule prose")
	}
	if strings.Contains(dashboard, "**Knowledge ref**") || strings.Contains(dashboard, "**Stale pointer**") {
		t.Error("the dashboard renders rule prose; only the rule files carry it")
	}
	if !strings.HasSuffix(dashboard, "\n") || strings.HasSuffix(dashboard, "\n\n") {
		t.Error("the dashboard should end with exactly one newline")
	}
}

func TestGenEmitsOnlyTheDashboard(t *testing.T) {
	repo, out := genRepo(t)

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("runGen = %d, stderr=%s", code, stderr.String())
	}

	// Exactly one generated artifact, and no generated views directory at all.
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("dashboard was not written: %v", err)
	}
	specDir := filepath.Join(repo.root, "docs", "spec")
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("read docs/spec: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "views" {
			t.Error("gen created a views directory; the per-rule files are the catalog")
		}
	}

	dashboard := readGenerated(t, out)
	for _, module := range catalogModules() {
		link := "[" + module + "](rules/" + module + "/)"
		if !strings.Contains(dashboard, link) {
			t.Errorf("dashboard has no link for module %s (want %q)", module, link)
		}
	}

	// The write reports the single artifact, not a view count.
	if !strings.Contains(stdout.String(), "wrote "+displayPath(out)) {
		t.Errorf("gen stdout = %q, want the dashboard path", stdout.String())
	}
	if strings.Contains(stdout.String(), "module views") || strings.Contains(stdout.String(), "views/") {
		t.Errorf("gen stdout = %q, want no module-view reporting", stdout.String())
	}
}

// The orphan machinery is gone: a views directory left over from the old
// split-artifact layout is not generated, not gated and not deleted.
func TestGenIgnoresAStaleViewsDirectory(t *testing.T) {
	repo, out := genRepo(t)
	repo.addFile(t, "docs/spec/views/game.md", "# a stale generated view\n")

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("gen = %d, stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff = %d, want 0 (a stale view is not a generated artifact): %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "views") || strings.Contains(stdout.String(), "orphan") {
		t.Errorf("diff output = %q, want no view or orphan reporting", stdout.String())
	}
	if got := readGenerated(t, filepath.Join(repo.root, "docs", "spec", "views", "game.md")); got != "# a stale generated view\n" {
		t.Errorf("gen touched a view file it no longer generates: %q", got)
	}
}

func TestGenDiff(t *testing.T) {
	repo, out := genRepo(t)

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("initial gen = %d, stderr=%s", code, stderr.String())
	}
	before := readGenerated(t, out)

	// In sync: exit 0, nothing written.
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff of in-sync output = %d, want 0 (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("diff message = %q, want an up-to-date notice", stdout.String())
	}

	// Stale: exit 1, and --diff must never write.
	catalog := genCatalog()
	catalog = append(catalog, baseRule())
	catalog[len(catalog)-1].ID = "R-KERNEL-002"
	repo.writeCatalog(t, catalog)
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff of stale output = %d, want 1 (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "stale") {
		t.Errorf("diff message = %q, want a stale notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "business-rules.md is stale") {
		t.Errorf("diff message = %q, want the dashboard named", stdout.String())
	}
	if after := readGenerated(t, out); after != before {
		t.Error("--diff modified the output file")
	}

	// A hand-edited dashboard is stale on its own.
	repo.writeCatalog(t, genCatalog())
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("regenerate = %d, stderr=%s", code, stderr.String())
	}
	if err := os.WriteFile(out, append([]byte(readGenerated(t, out)), "hand edit\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff of a hand-edited dashboard = %d, want 1 (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "business-rules.md is stale") {
		t.Errorf("diff message = %q, want the stale dashboard named", stdout.String())
	}

	// A missing dashboard is a change (exit 1), still no write, so the missing
	// file is not recreated.
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff of missing output = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "does not exist") {
		t.Errorf("diff message = %q, want a missing-file notice", stdout.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("--diff created %s", out)
	}
}

func TestGenRejectsUnexpectedArgs(t *testing.T) {
	repo, out := genRepo(t)
	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("runGen with extra arg = %d, want 2", code)
	}
}
