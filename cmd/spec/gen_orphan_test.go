package main

// Tests for orphan files in the generated views directory (gen.go
// diffArtifacts). `gen --diff` must gate the whole views/ directory, not only
// the files this run would write: a stray view (a module that was renamed, a
// hand-written note) is reported by name and fails the diff. Write mode reports
// it too but never deletes it — gen only ever writes the rendered artifacts.
//
// The output tree is synthetic (genRepo writes a throwaway checkout).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenDiffReportsOrphanView(t *testing.T) {
	repo, out, views := genRepo(t)

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("initial gen = %d, stderr=%s", code, stderr.String())
	}

	stray := filepath.Join(views, "stray.md")
	if err := os.WriteFile(stray, []byte("# a hand-written view\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out, "--diff"}, &stdout, &stderr); code != 1 {
		t.Fatalf("diff over an orphan view = %d, want 1 (stdout=%s stderr=%s)",
			code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"views/stray.md", "orphan"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("diff output %q does not mention %q", stdout.String(), want)
		}
	}
	// The rendered views are still reported as up to date; the orphan is the
	// only reason this run fails.
	if !strings.Contains(stdout.String(), "views/game.md is up to date") {
		t.Errorf("diff output %q does not report the rendered game view", stdout.String())
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("--diff removed the orphan: %v", err)
	}
}

func TestGenWriteModeReportsOrphanWithoutDeleting(t *testing.T) {
	repo, out, views := genRepo(t)
	repo.addFile(t, "docs/spec/views/stray.md", "# a hand-written view\n")

	var stdout, stderr bytes.Buffer
	if code := runGen([]string{"--rules", repo.rulesPath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("gen in write mode = %d, want 0 (orphans are reported, not fatal; stderr=%s)",
			code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "views/stray.md") || !strings.Contains(stderr.String(), "orphan") {
		t.Errorf("gen stderr = %q, want the orphan view named", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(views, "stray.md")); err != nil {
		t.Fatalf("gen deleted the orphan: %v", err)
	}
}
