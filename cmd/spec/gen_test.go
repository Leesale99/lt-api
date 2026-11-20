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
	kernel.Notes = "Context nobody needs. stale-pointer: TestDo moved in Phase B."
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
	money.Scope = ptr("row-local")
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
func genRepo(t *testing.T) (*testRepo, string, string) {
	t.Helper()
	repo := newTestRepo(t)
	repo.writeCatalog(t, genCatalog())
	out := filepath.Join(repo.root, "docs", "spec", "business-rules.md")
	return repo, out, filepath.Join(repo.root, "docs", "spec", "views")
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
	repo, out, views := genRepo(t)

	var first map[string][]byte
	for run := 0; run < 2; run++ {
		var stdout, stderr bytes.Buffer
		if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
			t.Fatalf("runGen run %d = %d, stderr=%s", run, code, stderr.String())
		}
		if run == 1 {
			continue
		}
		first = map[string][]byte{}
		first[out] = []byte(readGenerated(t, out))
		for _, module := range catalogModules() {
			view := filepath.Join(views, module+".md")
			first[view] = []byte(readGenerated(t, view))
		}
	}

	// Determinism: the second run is byte-identical for every artifact.
	for path, want := range first {
		if got := readGenerated(t, path); got != string(want) {
			t.Errorf("gen is not deterministic for %s:\n--- first ---\n%s\n--- second ---\n%s", path, want, got)
		}
	}

	dashboard := string(first[out])
	assertContains := func(md, want string) {
		t.Helper()
		if !strings.Contains(md, want) {
			t.Errorf("generated Markdown is missing %q", want)
		}
	}

	// Header: generated marker, canonical-source pointer, regeneration command.
	assertContains(dashboard, "GENERATED — do not edit")
	assertContains(dashboard, "# Business rules — dashboard")
	assertContains(dashboard, "canonical,\n> hand-edited catalog `"+displayPath(repo.rulesPath)+"`")
	assertContains(dashboard, "go run ./cmd/spec gen --rules "+displayPath(repo.rulesPath))

	// Dashboard: totals, counts, index — no rule blocks.
	assertContains(dashboard, "**4 rules** across 3 modules — 3 implemented, 1 specified-only.")
	for _, want := range []string{
		"| `game` | 2 |", "| `kernel` | 1 |", "| `platform` | 1 |", "| **total** | **4** |",
		"| `implemented` | 3 |", "| `specified-only` | 1 |",
		"| `game` | 2 | [`views/game.md`](views/game.md) |",
		"| `ledger` | 0 | [`views/ledger.md`](views/ledger.md) |",
		"| **total** | **4** | |",
	} {
		assertContains(dashboard, want)
	}
	if strings.Contains(dashboard, "#### R-GAME-001") {
		t.Error("the dashboard carries rule blocks; the rules belong to the module views")
	}

	// Refusal chains, including a null constraint/sentinel/http — game view.
	game := string(first[filepath.Join(views, "game.md")])
	for _, want := range []string{
		"# Business rules — `game`",
		"**2 rules** — 2 implemented.",
		"## Module `game`",
		"### Entity `ride`",
		"#### R-GAME-001 — Rides are born locked",
		"`23514` → `rides_money_state_coupling` → `ErrInvalidTransition` → `409`",
		"**Knowledge ref** `docs/league-tokens-learning-phases/phase-00-http-api-foundation.md`",
	} {
		assertContains(game, want)
	}
	if strings.Index(game, "#### R-GAME-001") > strings.Index(game, "#### R-GAME-002") {
		t.Error("rules within an entity are not sorted by id")
	}

	// The kernel view carries the stale-pointer note; the synthetic refusal
	// chain renders its null constraint/sentinel.
	kernel := string(first[filepath.Join(views, "kernel.md")])
	assertContains(kernel, "stale-pointer: TestDo moved in Phase B.")
	assertContains(kernel, "`synthetic` → _(unnamed)_ → _(unmapped)_ → `422`")

	// The platform view keeps the specified-only expert ref.
	platform := string(first[filepath.Join(views, "platform.md")])
	assertContains(platform, "**Expert ref** `docs/league-tokens-learning-phases/phase-03.md`")

	// Every generated artifact: ordinary notes stay out, one trailing newline.
	all := dashboard
	for _, module := range catalogModules() {
		view := string(first[filepath.Join(views, module+".md")])
		if strings.Contains(view, "Ordinary notes that should not reach the Markdown.") {
			t.Error("a module view leaked a non-rot notes block")
		}
		if !strings.HasSuffix(view, "\n") || strings.HasSuffix(view, "\n\n") {
			t.Errorf("view %q should end with exactly one newline", module)
		}
		all += view
	}
	if !strings.HasSuffix(dashboard, "\n") || strings.HasSuffix(dashboard, "\n\n") {
		t.Error("the dashboard should end with exactly one newline")
	}
	if got := strings.Count(all, "**Knowledge ref**"); got != 1 {
		t.Errorf("generated Markdown renders %d knowledge refs, want 1 (one rule carries the field)", got)
	}
}

func TestGenEmitsViewPerModule(t *testing.T) {
	repo, out, views := genRepo(t)

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("runGen = %d, stderr=%s", code, stderr.String())
	}

	want := []string{"game", "identity", "kernel", "ledger", "platform", "schedule"}
	entries, err := os.ReadDir(views)
	if err != nil {
		t.Fatalf("read views dir: %v", err)
	}
	var got []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".md" {
			got = append(got, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("views emitted = %v, want one per module %v", got, want)
	}

	// A module with no rule still gets a view, so the dashboard index never
	// dangles.
	ledger := readGenerated(t, filepath.Join(views, "ledger.md"))
	for _, want := range []string{"# Business rules — `ledger`", "## Module `ledger`", "_(no rule in this module yet)_"} {
		if !strings.Contains(ledger, want) {
			t.Errorf("empty module view is missing %q", want)
		}
	}

	// The write reports both artifacts.
	for _, want := range []string{"wrote ", displayPath(out), "6 module views"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("gen stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestGenDiff(t *testing.T) {
	repo, out, views := genRepo(t)
	gameView := filepath.Join(views, "game.md")

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
	if !strings.Contains(stdout.String(), "views/kernel.md is stale") {
		t.Errorf("diff message = %q, want the stale kernel view named", stdout.String())
	}
	if after := readGenerated(t, out); after != before {
		t.Error("--diff modified the output file")
	}

	// A hand-edited view is stale on its own: the views are gated too.
	repo.writeCatalog(t, genCatalog())
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("regenerate = %d, stderr=%s", code, stderr.String())
	}
	if err := os.WriteFile(gameView, append([]byte(readGenerated(t, gameView)), "hand edit\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff of a hand-edited view = %d, want 1 (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "views/game.md is stale") {
		t.Errorf("diff message = %q, want the stale game view named", stdout.String())
	}

	// Missing dashboard and missing view: both a change (exit 1), still no
	// write, so the missing file is not recreated.
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gameView); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff of missing output = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "does not exist") {
		t.Errorf("diff message = %q, want a missing-file notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "views/game.md does not exist") {
		t.Errorf("diff message = %q, want the missing game view named", stdout.String())
	}
	for _, path := range []string{out, gameView} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("--diff created %s", path)
		}
	}
}

func TestGenRejectsUnexpectedArgs(t *testing.T) {
	repo, out, _ := genRepo(t)
	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("runGen with extra arg = %d, want 2", code)
	}
}
