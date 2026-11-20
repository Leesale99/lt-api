package main

// Pins for `spec brief`: the enforcement resolution column and the block-quote
// escaping. They are separate from brief_test.go so the red-first change to the
// reading aid is visible on its own.

import (
	"strings"
	"testing"
)

// TestBriefEnforcementResolvesColumn pins writeBriefEnforcement's Resolves
// column: every site is resolved the way `check` resolves it today, and a
// site that is malformed or absent is **missing**.
func TestBriefEnforcementResolvesColumn(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "migrations/000001_create_initial_game_models.up.sql", "-- schema\n")
	repo.addFile(t, "internal/kernel/thing/gadget.go",
		"package thing\n\ntype Widget struct{}\n\ntype Gadget struct{}\n\nfunc (g *Gadget) Run() {}\n")

	catalog := briefCatalog()
	rule := catalog[1] // R-KERNEL-001
	rule.Enforcement = []Enforcement{
		{Site: "internal/kernel/thing/thing.go::Do", Layer: "domain", Role: "authoritative", PinnedBy: ptr("rideBirthRule")},
		{Site: "internal/kernel/thing/gadget.go::Gadget.Run", Layer: "domain", Role: "authoritative", PinnedBy: ptr("rideBirthRule")},
		{Site: "internal/kernel/thing/gadget.go::Widget.Run", Layer: "domain", Role: "authoritative", PinnedBy: ptr("rideBirthRule")},
		{Site: "migrations/000001_create_initial_game_models.up.sql", Layer: "db-constraint", Role: "authoritative", PinnedBy: ptr("rideBirthRule")},
		{Site: "migrations/000002_gone.up.sql", Layer: "db-constraint", Role: "authoritative", PinnedBy: ptr("rideBirthRule")},
		{Site: "not-a-pointer", Layer: "domain", Role: "authoritative"},
	}
	catalog[1] = rule
	repo.writeCatalog(t, catalog)

	code, out, errOut := runBriefTest(t, repo, "R-KERNEL-001")
	if code != 0 {
		t.Fatalf("runBrief = %d, stderr=%s", code, errOut)
	}

	for _, want := range []string{
		"| Site | Layer | Role | Pinned by | Resolves |",
		"| `internal/kernel/thing/thing.go::Do` | `domain` | `authoritative` | `rideBirthRule` | yes |",
		"| `internal/kernel/thing/gadget.go::Gadget.Run` | `domain` | `authoritative` | `rideBirthRule` | yes |",
		"| `internal/kernel/thing/gadget.go::Widget.Run` | `domain` | `authoritative` | `rideBirthRule` | **missing** |",
		"| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | `rideBirthRule` | yes |",
		"| `migrations/000002_gone.up.sql` | `db-constraint` | `authoritative` | `rideBirthRule` | **missing** |",
		"| `not-a-pointer` | `domain` | `authoritative` | — | **missing** |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief is missing %q", want)
		}
	}
}

// TestBriefQuoteBlockEscapesMarkdownStructure pins the escaping writeQuoteBlock
// applies to note lines that would otherwise change the Markdown structure of
// the quote: a nested quote, a heading, and a list marker.
func TestBriefQuoteBlockEscapesMarkdownStructure(t *testing.T) {
	notes := strings.Join([]string{
		"Plain prose stays exactly as written.",
		"> a nested quote",
		"# a heading",
		"## another heading",
		"- a list item",
		"* another list item",
		"+ a third list item",
		"1. an ordered item",
		"12. another ordered item",
		"2026 was a year, not a list.",
		"-not-a-list-marker",
	}, "\n")

	var b strings.Builder
	writeQuoteBlock(&b, notes)
	out := b.String()

	for _, want := range []string{
		"> Plain prose stays exactly as written.",
		"> \\> a nested quote",
		"> \\# a heading",
		"> \\## another heading",
		"> \\- a list item",
		"> \\* another list item",
		"> \\+ a third list item",
		"> 1\\. an ordered item",
		"> 12\\. another ordered item",
		"> 2026 was a year, not a list.",
		"> -not-a-list-marker",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("quote block is missing %q; got:\n%s", want, out)
		}
	}
}

// TestBriefNotesEscapesMarkdownStructure keeps the generated catalog views on
// the same escaping as the brief: both render notes as the same block quote.
func TestBriefNotesEscapesMarkdownStructure(t *testing.T) {
	var b strings.Builder
	writeNotes(&b, "# a heading\n> a nested quote\n- a list item\n1. an ordered item")
	out := b.String()

	for _, want := range []string{
		"**Notes**",
		"> \\# a heading",
		"> \\> a nested quote",
		"> \\- a list item",
		"> 1\\. an ordered item",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("notes block is missing %q; got:\n%s", want, out)
		}
	}
}
