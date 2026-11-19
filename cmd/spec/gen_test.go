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

func TestGenDeterministic(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, genCatalog())

	out := filepath.Join(repo.root, "docs", "spec", "business-rules.md")

	var first []byte
	for run := 0; run < 2; run++ {
		var stdout, stderr bytes.Buffer
		if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
			t.Fatalf("runGen run %d = %d, stderr=%s", run, code, stderr.String())
		}
		generated, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first = generated
			continue
		}
		if !bytes.Equal(first, generated) {
			t.Fatalf("gen is not deterministic across runs:\n--- first ---\n%s\n--- second ---\n%s", first, generated)
		}
	}

	md := string(first)
	assertContains := func(want string) {
		t.Helper()
		if !strings.Contains(md, want) {
			t.Errorf("generated Markdown is missing %q", want)
		}
	}

	// Header: generated marker, canonical-source pointer, regeneration command.
	assertContains("GENERATED — do not edit")
	assertContains("canonical,\n> hand-edited catalog `" + displayPath(repo.rulesPath) + "`")
	assertContains("go run ./cmd/spec gen --rules " + displayPath(repo.rulesPath))

	// Dashboard counts.
	assertContains("**4 rules** across 3 modules.")
	for _, want := range []string{
		"| `game` | 2 |", "| `kernel` | 1 |", "| `platform` | 1 |", "| **total** | **4** |",
		"| `implemented` | 3 |", "| `specified-only` | 1 |",
		"| `interface` | 1 |", "| `invariant` | 1 |", "| `orchestration` | 1 |", "| `policy` | 1 |",
		"| `database` | 1 |", "| `domain` | 1 |", "| `handler` | 1 |", "| `service` | 1 |",
	} {
		assertContains(want)
	}

	// Refusal chains, including a null constraint/sentinel/http.
	assertContains("`synthetic` → _(unnamed)_ → _(unmapped)_ → `422`")
	assertContains("`23514` → `rides_money_state_coupling` → `ErrInvalidTransition` → `409`")

	// Notes: stale-pointer notes survive, ordinary notes stay out.
	assertContains("stale-pointer: TestDo moved in Phase B.")
	if strings.Contains(md, "Ordinary notes that should not reach the Markdown.") {
		t.Error("generated Markdown leaked a non-rot notes block")
	}

	// Expert ref for specified-only rules.
	assertContains("**Expert ref** `docs/league-tokens-learning-phases/phase-03.md`")

	// Deterministic ordering: modules, then ids.
	ordered := []string{"## Module `game`", "## Module `kernel`", "## Module `platform`"}
	last := -1
	for _, marker := range ordered {
		idx := strings.Index(md, marker)
		if idx < 0 {
			t.Fatalf("missing %q", marker)
		}
		if idx <= last {
			t.Fatalf("module %q is out of order", marker)
		}
		last = idx
	}
	if strings.Index(md, "#### R-GAME-001") > strings.Index(md, "#### R-GAME-002") {
		t.Error("rules within an entity are not sorted by id")
	}

	if !strings.HasSuffix(md, "\n") || strings.HasSuffix(md, "\n\n") {
		t.Error("generated Markdown should end with exactly one newline")
	}
}

func TestGenDiff(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, genCatalog())
	out := filepath.Join(repo.root, "docs", "spec", "business-rules.md")

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("initial gen = %d, stderr=%s", code, stderr.String())
	}
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

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
	after, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("--diff modified the output file")
	}

	// Missing output: also a change (exit 1), still no write.
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
		t.Error("--diff created the output file")
	}
}

func TestGenRejectsUnexpectedArgs(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, genCatalog())
	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("runGen with extra arg = %d, want 2", code)
	}
}
