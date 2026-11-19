package main

// Tests for per-spec docs inside grouped var/const/type declarations
// (annotations.go declarationsOf). Inside a `var (...)` / `const (...)` /
// `type (...)` block, the comment directly above one spec belongs to that spec,
// not to the whole block: an annotation there must satisfy that spec's site,
// while a spec without its own doc falls back to the block-level doc.
//
// The synthetic trees are built under the OS temp dir: the grouped declarations
// are fixtures for the checker, never repository code.

import (
	"strings"
	"testing"
)

func TestAnnotationsReadGroupedSpecDocs(t *testing.T) {
	const site = "internal/kernel/thing/group.go::Widget"

	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "var with block-level doc",
			source: "package thing\n\n// Group doc.\nvar (\n\t//rule:R-KERNEL-001\n\tWidget = 1\n)\n",
		},
		{
			name:   "var without block-level doc",
			source: "package thing\n\nvar (\n\t//rule:R-KERNEL-001\n\tWidget = 1\n)\n",
		},
		{
			name:   "const with block-level doc",
			source: "package thing\n\n// Group doc.\nconst (\n\t//rule:R-KERNEL-001\n\tWidget = 1\n)\n",
		},
		{
			name:   "const without block-level doc",
			source: "package thing\n\nconst (\n\t//rule:R-KERNEL-001\n\tWidget = 1\n)\n",
		},
		{
			name:   "type with block-level doc",
			source: "package thing\n\n// Group doc.\ntype (\n\t//rule:R-KERNEL-001\n\tWidget struct{}\n)\n",
		},
		{
			name:   "type without block-level doc",
			source: "package thing\n\ntype (\n\t//rule:R-KERNEL-001\n\tWidget struct{}\n)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			repo.addFile(t, "internal/kernel/thing/group.go", tt.source)

			catalog := rulesNaming(site, "R-KERNEL-001")
			if findings := collectAnnotations(catalog, repo.root); len(findings) != 0 {
				t.Fatalf("the annotation above the grouped spec was not read:\n%s",
					describeFindings(findings))
			}
		})
	}
}

// TestAnnotationsGroupedSiblingDoesNotCoverUnannotatedSpec is the negative: an
// annotation on one grouped spec must not leak to its sibling, even though both
// share the block-level doc (here absent). The sibling without its own
// annotation stays a missing-annotation error.
func TestAnnotationsGroupedSiblingDoesNotCoverUnannotatedSpec(t *testing.T) {
	const path = "internal/kernel/thing/group.go"
	repo := newTestRepo(t)
	repo.addFile(t, path, `package thing

var (
	//rule:R-KERNEL-002
	Widget = 1
	Gadget = 2
)
`)

	catalog := append(
		rulesNaming(path+"::Gadget", "R-KERNEL-001"),
		rulesNaming(path+"::Widget", "R-KERNEL-002")...,
	)
	findings := collectAnnotations(catalog, repo.root)

	if len(findings) != 1 {
		t.Fatalf("%d findings, want 1 (Gadget is unannotated):\n%s",
			len(findings), describeFindings(findings))
	}
	f := findings[0]
	if f.RuleID != "R-KERNEL-001" || !strings.Contains(f.Message, "::Gadget") {
		t.Errorf("finding = %+v, want the missing R-KERNEL-001 annotation on Gadget", f)
	}
	if strings.Contains(f.Message, "::Widget") {
		t.Errorf("the sibling's annotation leaked to Widget: %q", f.Message)
	}
}
