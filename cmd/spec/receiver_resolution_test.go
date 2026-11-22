package main

// Tests for receiver-aware symbol resolution (check.go symbolExists) and the
// fail-closed half of policy 15 (annotations.go checkSitesAnnotated).
//
// The blind spot they pin: symbol matching used to be receiver-agnostic, so a
// catalog site saying `Widget.Run` passed when the file declared only
// `func (g *Gadget) Run(` — policies 3 and 15 both passed silently. The tree
// below is synthetic and built under the OS temp dir on purpose: it holds Go
// files that must never be scanned by the repository's own checker.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// receiverRule is a minimal, otherwise-valid implemented rule whose single site
// names `Widget.Run`.
func receiverRule() Rule {
	return Rule{
		ID:        "R-TEST-001",
		Title:     "A widget runs only itself",
		Statement: "A widget runs. A gadget never answers for a widget.",
		Module:    "kernel",
		Entity:    "widget",
		Category:  "orchestration",
		Status:    statusImplemented,
		Authority: "service",
		Enforcement: []Enforcement{{
			Site:  "pkg/widget.go::Widget.Run",
			Layer: "domain",
			Role:  "authoritative",
		}},
		Verification: Verification{
			Unit: []string{"pkg/widget_test.go::TestRun"},
		},
	}
}

// newReceiverTree writes a throwaway checkout under /tmp: the catalog (loaded
// back through loadCatalog, so strict decoding stays exercised) plus the Go
// source under test and its unit-test pointer.
func newReceiverTree(t *testing.T, source string) (root string, catalog Catalog) {
	t.Helper()

	root, err := os.MkdirTemp("", "spec-receiver-*")
	if err != nil {
		t.Fatalf("create temp tree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("pkg/widget.go", source)
	write("pkg/widget_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {}\n")

	data, err := yaml.Marshal(Catalog{receiverRule()})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	write("docs/spec/rules.yaml", string(data))

	catalog, err = loadCatalog(filepath.Join(root, "docs", "spec", "rules.yaml"))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return root, catalog
}

// TestSymbolExistsIsReceiverAware pins the matcher itself: `Type.Name` must name
// the receiver type the file actually declares, while a bare name keeps the
// receiver-agnostic read.
func TestSymbolExistsIsReceiverAware(t *testing.T) {
	src := `package thing

type Widget struct{}

type Gadget struct{}

type Repository[T any] struct{}

func (g *Gadget) Run() {}

func (Widget) Stop() {}

func (r *Repository[T]) Save(item T) {}

func Helper() {}

func Generic[T any](v T) {}
`
	tests := []struct {
		symbol string
		want   bool
	}{
		{"Widget.Run", false},
		{"Gadget.Run", true},
		{"Widget.Stop", true}, // unnamed receiver
		{"Gadget.Stop", false},
		{"Repository.Save", true}, // generic receiver
		{"Widget.Save", false},
		{"Run", true}, // a bare name stays receiver-agnostic
		{"Helper", true},
		{"Generic", true},
		{"Widget.Missing", false},
	}
	for _, tt := range tests {
		if got := symbolExists(src, tt.symbol); got != tt.want {
			t.Errorf("symbolExists(%q) = %v, want %v", tt.symbol, got, tt.want)
		}
	}
}

// TestReceiverResolutionAtEnforcementSite is the three-case regression: a site
// naming `Widget.Run` against a file that declares only `Gadget.Run` must fail
// both ways — policy 3 (the symbol is not there) and policy 15 (the site is
// never a silent pass). The annotation on the wrong declaration does not save
// it, and the correct declaration with its annotation is clean.
func TestReceiverResolutionAtEnforcementSite(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		wantPolicy3      string
		wantPolicy15     string
		wantPolicy15File string
	}{
		{
			name:             "no annotation, receiver mismatch",
			source:           "package pkg\n\nfunc (g *Gadget) Run() {}\n",
			wantPolicy3:      `symbol "Widget.Run" not found`,
			wantPolicy15:     `widget.go::Widget.Run`,
			wantPolicy15File: "pkg/widget.go",
		},
		{
			name:             "annotation on the wrong receiver",
			source:           "package pkg\n\n//rule:R-TEST-001\nfunc (g *Gadget) Run() {}\n",
			wantPolicy3:      `symbol "Widget.Run" not found`,
			wantPolicy15:     `widget.go::Widget.Run`,
			wantPolicy15File: "pkg/widget.go",
		},
		{
			name:   "the declared receiver carries the annotation",
			source: "package pkg\n\n//rule:R-TEST-001\nfunc (w *Widget) Run() {}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, catalog := newReceiverTree(t, tt.source)

			findings := collectFindings(catalog, root)
			findings = append(findings, collectAnnotations(catalog, root)...)

			got3 := findingsFor(findings, "3-implemented")
			got15 := findingsFor(findings, policyAnnotations)

			if tt.wantPolicy3 == "" {
				if len(got3) != 0 {
					t.Errorf("unexpected policy-3 findings:\n%s", describeFindings(got3))
				}
			} else if len(got3) != 1 || !strings.Contains(got3[0].Message, tt.wantPolicy3) {
				t.Errorf("policy-3 findings = %d, want 1 mentioning %q:\n%s",
					len(got3), tt.wantPolicy3, describeFindings(findings))
			}

			if tt.wantPolicy15 == "" {
				if len(got15) != 0 {
					t.Errorf("unexpected policy-15 findings:\n%s", describeFindings(got15))
				}
			} else if len(got15) != 1 || !strings.Contains(got15[0].Message, tt.wantPolicy15) {
				t.Errorf("policy-15 findings = %d, want 1 mentioning %q:\n%s",
					len(got15), tt.wantPolicy15, describeFindings(findings))
			} else {
				if got15[0].Subject != "R-TEST-001" {
					t.Errorf("policy-15 finding attributed to %q, want R-TEST-001", got15[0].Subject)
				}
				if !strings.Contains(got15[0].Message, tt.wantPolicy15File) {
					t.Errorf("policy-15 message %q does not name %q", got15[0].Message, tt.wantPolicy15File)
				}
			}

			if tt.wantPolicy3 == "" && tt.wantPolicy15 == "" && len(findings) != 0 {
				t.Errorf("a correctly declared and annotated site produced findings:\n%s",
					describeFindings(findings))
			}
		})
	}
}
