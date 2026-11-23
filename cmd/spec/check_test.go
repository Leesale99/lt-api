package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func findingsFor(findings []Finding, policyID string) []Finding {
	var group []Finding
	for _, f := range findings {
		if f.Policy == policyID {
			group = append(group, f)
		}
	}
	return group
}

func describeFindings(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(fmt.Sprintf("[%s] %s %s: %s\n", f.Policy, f.Severity, f.Subject, f.Message))
	}
	return b.String()
}

func TestCheckValidCatalogHasNoFindings(t *testing.T) {
	repo := newTestRepo(t)
	findings := collectFindings(baseCatalog(), repo.root)
	if len(findings) != 0 {
		t.Fatalf("valid catalog produced findings:\n%s", describeFindings(findings))
	}
}

func TestCheckPolicyViolations(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*Rule)
		extra        []Rule
		wantPolicy   string
		wantErrors   int
		wantWarnings int
		wantMessage  string
	}{
		{
			name:       "id format",
			mutate:     func(r *Rule) { r.ID = "R-KERNEL-1" },
			wantPolicy: "1-id", wantErrors: 1,
			wantMessage: "does not match",
		},
		{
			name:       "duplicate id",
			extra:      []Rule{baseRule()},
			wantPolicy: "1-id", wantErrors: 1,
			wantMessage: "duplicate id",
		},
		{
			name:       "implemented without enforcement",
			mutate:     func(r *Rule) { r.Enforcement = nil },
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "no enforcement site",
		},
		{
			name:       "implemented without verification",
			mutate:     func(r *Rule) { r.Verification = Verification{} },
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "no verification pointer",
		},
		{
			// Policy 3 counts every proof: a business-visible rule proved by an
			// executable scenario needs no Go test pointer.
			name: "gherkin pointer alone satisfies policy 3",
			mutate: func(r *Rule) {
				r.Verification = Verification{
					Gherkin: []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"},
				}
			},
		},
		{
			name: "e2e pointer alone satisfies policy 3",
			mutate: func(r *Rule) {
				r.Verification = Verification{E2E: []string{"internal/kernel/thing/thing_test.go::TestDo"}}
			},
		},
		{
			// A gherkin pointer is a proof, not a shield: the Go pointers that
			// are present are still resolved against the tree.
			name: "gherkin pointer does not mask a dead unit pointer",
			mutate: func(r *Rule) {
				r.Verification = Verification{
					Unit:    []string{"internal/kernel/thing/nope_test.go::TestDo"},
					Gherkin: []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"},
				}
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "verification path",
		},
		{
			name: "verification path missing",
			mutate: func(r *Rule) {
				r.Verification = Verification{Unit: []string{"internal/kernel/thing/nope_test.go::TestDo"}}
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "verification path",
		},
		{
			name: "verification symbol missing",
			mutate: func(r *Rule) {
				r.Verification = Verification{Unit: []string{"internal/kernel/thing/other.go::TestDo"}}
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "verification symbol",
		},
		{
			name: "verification symbol is not a test function",
			mutate: func(r *Rule) {
				r.Verification = Verification{Unit: []string{"internal/kernel/thing/other.go::Token"}}
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "verification symbol",
		},
		{
			name: "enforcement path missing",
			mutate: func(r *Rule) {
				r.Enforcement[0].Site = "internal/kernel/thing/nope.go::Do"
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "enforcement path",
		},
		{
			name: "enforcement symbol missing",
			mutate: func(r *Rule) {
				r.Enforcement[0].Site = "internal/kernel/thing/thing.go::Nope"
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "enforcement symbol",
		},
		{
			name: "enforcement migration missing",
			mutate: func(r *Rule) {
				r.Enforcement[0].Site = "migrations/000099_missing.up.sql"
			},
			wantPolicy: "3-implemented", wantErrors: 1,
			wantMessage: "does not exist",
		},
		{
			name: "specified-only without expert-ref",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Enforcement = nil
				r.Verification = Verification{}
			},
			wantPolicy: "4-specified-only", wantErrors: 1,
			wantMessage: "no expert-ref",
		},
		{
			name: "specified-only expert-ref missing",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Enforcement = nil
				r.Verification = Verification{}
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/nope.md")
			},
			wantPolicy: "4-specified-only", wantErrors: 1,
			wantMessage: "does not exist",
		},
		{
			name: "specified-only with enforcement",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Verification = Verification{}
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
			},
			wantPolicy: "4-specified-only", wantErrors: 1,
			wantMessage: "enforcement site",
		},
		{
			name: "specified-only with verification",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Enforcement = nil
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
			},
			wantPolicy: "4-specified-only", wantErrors: 1,
			wantMessage: "verification pointers",
		},
		{
			name: "specified-only with refusal",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Enforcement = nil
				r.Verification = Verification{}
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
				r.Refusal = &Refusal{Code: "synthetic"}
			},
			wantPolicy: "4-specified-only", wantErrors: 1,
			wantMessage: "has a refusal",
		},
		{
			name: "drifted without notes",
			mutate: func(r *Rule) {
				r.Status = statusDrifted
				r.Notes = ""
			},
			wantPolicy: "5-drifted", wantErrors: 1,
			wantMessage: "no notes",
		},
		{
			name: "stale-pointer on specified-only",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Enforcement = nil
				r.Verification = Verification{}
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
				r.StalePointer = ptr("a dead test pointer")
			},
			wantPolicy: "5-drifted", wantErrors: 1,
			wantMessage: "stale-pointer",
		},
		{
			name: "database without mechanism",
			mutate: func(r *Rule) {
				r.Authority = "database"
				r.WriteScope = ptr("row-local")
				r.Notes = "database authority because the predicate is row-local"
			},
			wantPolicy: "6-authority", wantErrors: 1,
			wantMessage: "requires a mechanism",
		},
		{
			name: "database without scope",
			mutate: func(r *Rule) {
				r.Authority = "database"
				r.Mechanism = ptr("constraint")
				r.Notes = "database authority because the predicate is row-local"
			},
			wantPolicy: "6-authority", wantErrors: 1,
			wantMessage: "requires a write_scope",
		},
		{
			name: "non-database with mechanism",
			mutate: func(r *Rule) {
				r.Mechanism = ptr("constraint")
			},
			wantPolicy: "6-authority", wantErrors: 1,
			wantMessage: "must not set a mechanism",
		},
		{
			name: "defense-in-depth without tie",
			mutate: func(r *Rule) {
				r.Enforcement[0].Role = "defense-in-depth"
			},
			wantPolicy: "7-copy-policy", wantErrors: 1,
			wantMessage: "no pinned_by and no tie note",
		},
		{
			name: "authority deviates without notes",
			mutate: func(r *Rule) {
				r.Authority = "handler"
			},
			wantPolicy: "8-authority-default", wantErrors: 1,
			wantMessage: "without notes",
		},
		{
			name: "bare enforcement path",
			mutate: func(r *Rule) {
				r.Enforcement[0].Site = "internal/kernel/thing/thing.go"
			},
			wantPolicy: "9-site-ref", wantErrors: 1,
			wantMessage: "neither a migrations/ id nor path::Symbol",
		},
		{
			name: "line-number enforcement site",
			mutate: func(r *Rule) {
				r.Enforcement[0].Site = "internal/kernel/thing/thing.go:12::Do"
			},
			wantPolicy: "9-site-ref", wantErrors: 1,
			wantMessage: "uses a line number",
		},
		{
			name: "bare verification path",
			mutate: func(r *Rule) {
				r.Verification = Verification{Unit: []string{"internal/kernel/thing/thing_test.go"}}
			},
			wantPolicy: "9-site-ref", wantErrors: 1,
			wantMessage: "not path::Symbol",
		},
		{
			name: "line-number verification pointer",
			mutate: func(r *Rule) {
				r.Verification = Verification{Unit: []string{"internal/kernel/thing/thing_test.go:12::TestDo"}}
			},
			wantPolicy: "9-site-ref", wantErrors: 1,
			wantMessage: "uses a line number",
		},
		// The gherkin pointer policy (10-gherkin) is exercised against real
		// feature files in gherkin_test.go — it needs the parser, not just the
		// catalog.
		{
			name: "undeclared fixture",
			mutate: func(r *Rule) {
				r.Enforcement[0].Role = "defense-in-depth"
				r.Enforcement[0].PinnedBy = ptr("nope")
				r.Tie = ptr("the authority refuses the wrong copy.")
			},
			wantPolicy: "11-fixture", wantErrors: 1,
			wantMessage: "not a fixture",
		},
		{
			name:       "refusal code not a SQLSTATE",
			mutate:     func(r *Rule) { r.Refusal = &Refusal{Code: "nope"} },
			wantPolicy: "17-refusal", wantErrors: 1,
			wantMessage: "neither 'synthetic' nor a 5-character SQLSTATE",
		},
		{
			name:       "refusal without code",
			mutate:     func(r *Rule) { r.Refusal = &Refusal{} },
			wantPolicy: "17-refusal", wantErrors: 1,
			wantMessage: "has no code",
		},
		{
			name:         "refusal outside the illustrative set warns",
			mutate:       func(r *Rule) { r.Refusal = &Refusal{Code: "23505"} },
			wantPolicy:   "17-refusal",
			wantWarnings: 1,
			wantMessage:  "outside the illustrative set",
		},
		// Valid counterparts: these must stay silent.
		{
			name:   "illustrative SQLSTATE accepted",
			mutate: func(r *Rule) { r.Refusal = &Refusal{Code: "P0001"} },
		},
		{
			name:   "synthetic refusal accepted",
			mutate: func(r *Rule) { r.Refusal = &Refusal{Code: "synthetic"} },
		},
		{
			name: "defense-in-depth tied by a tie note",
			mutate: func(r *Rule) {
				r.Enforcement[0].Role = "defense-in-depth"
				r.Tie = ptr("the authority refuses the wrong copy.")
			},
		},
		{
			name: "defense-in-depth pinned by declared fixture",
			mutate: func(r *Rule) {
				r.Enforcement[0].Role = "defense-in-depth"
				r.Enforcement[0].PinnedBy = ptr("sharedRule")
				r.Fixture = ptr("sharedRule")
			},
		},
		{
			name: "advisory-copy exempt",
			mutate: func(r *Rule) {
				r.Enforcement[0].Role = "advisory-copy"
			},
		},
		{
			// Gate B5 added composition-root to the layer enum (cross-context
			// wiring, distinguished from handler's transport framing).
			name: "composition-root layer accepted",
			mutate: func(r *Rule) {
				r.Enforcement[0].Layer = "composition-root"
			},
		},
		{
			name: "database authority with notes",
			mutate: func(r *Rule) {
				r.Authority = "database"
				r.Mechanism = ptr("constraint")
				r.WriteScope = ptr("row-local")
				r.Notes = "database authority because the predicate is row-local"
			},
		},
		{
			name: "drifted with notes",
			mutate: func(r *Rule) {
				r.Status = statusDrifted
				r.Notes = "the docs allow what the code refuses"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			rule := baseRule()
			if tt.mutate != nil {
				tt.mutate(&rule)
			}
			catalog := append(Catalog{rule}, tt.extra...)
			findings := collectFindings(catalog, repo.root)

			gotErrors, gotWarnings := countFindings(findings)
			if gotErrors != tt.wantErrors || gotWarnings != tt.wantWarnings {
				t.Fatalf("got %d errors / %d warnings, want %d / %d:\n%s",
					gotErrors, gotWarnings, tt.wantErrors, tt.wantWarnings, describeFindings(findings))
			}
			if tt.wantPolicy == "" {
				return
			}
			group := findingsFor(findings, tt.wantPolicy)
			if len(group) != tt.wantErrors+tt.wantWarnings {
				t.Fatalf("policy %s carried %d findings, want %d:\n%s",
					tt.wantPolicy, len(group), tt.wantErrors+tt.wantWarnings, describeFindings(findings))
			}
			if len(group) > 0 && !strings.Contains(group[0].Message, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", group[0].Message, tt.wantMessage)
			}
		})
	}
}

func TestCheckEnumMembership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Rule)
	}{
		{"module", func(r *Rule) { r.Module = "bogus" }},
		// An unknown category has no default authority, so policy 8 stays quiet.
		{"category", func(r *Rule) { r.Category = "bogus" }},
		{"status", func(r *Rule) { r.Status = "bogus" }},
		{"authority", func(r *Rule) {
			r.Category = "refusal-mapping" // no default authority
			r.Authority = "bogus"
		}},
		{"mechanism", func(r *Rule) {
			r.Authority = "database"
			r.Mechanism = ptr("bogus")
			r.WriteScope = ptr("row-local")
			r.Notes = "database authority by design"
		}},
		{"write_scope", func(r *Rule) {
			r.Authority = "database"
			r.Mechanism = ptr("constraint")
			r.WriteScope = ptr("bogus")
			r.Notes = "database authority by design"
		}},
		{"layer", func(r *Rule) { r.Enforcement[0].Layer = "bogus" }},
		{"role", func(r *Rule) { r.Enforcement[0].Role = "bogus" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			rule := baseRule()
			tt.mutate(&rule)
			findings := collectFindings(Catalog{rule}, repo.root)

			if errorsFound, warnings := countFindings(findings); errorsFound != 1 || warnings != 0 {
				t.Fatalf("got %d errors / %d warnings, want exactly 1 error:\n%s",
					errorsFound, warnings, describeFindings(findings))
			}
			group := findingsFor(findings, "2-enums")
			if len(group) != 1 {
				t.Fatalf("enum finding missing:\n%s", describeFindings(findings))
			}
			if !strings.Contains(group[0].Message, "not a frozen enum value") {
				t.Errorf("message = %q, want an enum-membership complaint", group[0].Message)
			}
		})
	}
}

func TestCheckRefusalWarnings(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		wantErrors  int
		wantWarns   int
		wantMessage string
	}{
		{"illustrative P0001", "P0001", 0, 0, ""},
		{"illustrative 23514", "23514", 0, 0, ""},
		{"illustrative 23503", "23503", 0, 0, ""},
		{"illustrative 23001", "23001", 0, 0, ""},
		{"synthetic", "synthetic", 0, 0, ""},
		{"unique violation", "23505", 0, 1, "outside the illustrative set"},
		{"insufficient privilege", "42501", 0, 1, "outside the illustrative set"},
		{"malformed", "P1", 1, 0, "SQLSTATE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			rule := baseRule()
			rule.Refusal = &Refusal{Code: tt.code}
			findings := collectFindings(Catalog{rule}, repo.root)

			errorsFound, warnings := countFindings(findings)
			if errorsFound != tt.wantErrors || warnings != tt.wantWarns {
				t.Fatalf("got %d errors / %d warnings, want %d / %d:\n%s",
					errorsFound, warnings, tt.wantErrors, tt.wantWarns, describeFindings(findings))
			}
			if tt.wantMessage != "" {
				group := findingsFor(findings, "17-refusal")
				if len(group) == 0 || !strings.Contains(group[0].Message, tt.wantMessage) {
					t.Fatalf("expected a refusal finding containing %q:\n%s",
						tt.wantMessage, describeFindings(findings))
				}
			}
		})
	}
}

func runCheckForTest(t *testing.T, repo *testRepo, strict bool) (int, string) {
	t.Helper()
	args := []string{"--rules", repo.rulesPath}
	if strict {
		args = append(args, "--strict")
	}
	var stdout, stderr bytes.Buffer
	code := runCheck(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestCheckExitCodes(t *testing.T) {
	t.Run("clean catalog is report-only and strict-green", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.writeCatalog(t, baseCatalog())

		code, output := runCheckForTest(t, repo, false)
		if code != 0 {
			t.Fatalf("report-only check on a clean catalog = %d, want 0", code)
		}
		if !strings.Contains(output, "No findings") {
			t.Errorf("clean catalog output lacks a no-findings message:\n%s", output)
		}
		if !strings.Contains(output, "| `1-id` | 0 | 0 |") {
			t.Errorf("summary table missing zero rows:\n%s", output)
		}

		if code, _ := runCheckForTest(t, repo, true); code != 0 {
			t.Fatalf("strict check on a clean catalog = %d, want 0", code)
		}
	})

	t.Run("errors are report-only but fail strict", func(t *testing.T) {
		repo := newTestRepo(t)
		broken := baseRule()
		broken.Verification = Verification{}
		repo.writeCatalog(t, Catalog{broken})

		code, output := runCheckForTest(t, repo, false)
		if code != 0 {
			t.Fatalf("report-only check = %d, want 0", code)
		}
		if !strings.Contains(output, "## 3-implemented") || !strings.Contains(output, "report-only") {
			t.Errorf("output does not look like a grouped report:\n%s", output)
		}

		if code, _ := runCheckForTest(t, repo, true); code != 1 {
			t.Fatalf("strict check = %d, want 1", code)
		}
	})

	t.Run("warnings are printed but never fail strict", func(t *testing.T) {
		repo := newTestRepo(t)
		warned := baseRule()
		warned.Refusal = &Refusal{Code: "23505", HTTP: ptr(409)}
		repo.writeCatalog(t, Catalog{warned})

		code, output := runCheckForTest(t, repo, true)
		if code != 0 {
			t.Fatalf("strict check with warnings only = %d, want 0:\n%s", code, output)
		}
		if !strings.Contains(output, "WARN **R-KERNEL-001**") {
			t.Errorf("WARN line missing:\n%s", output)
		}
		if !strings.Contains(output, "| `17-refusal` | 0 | 1 |") {
			t.Errorf("warning count missing from the summary:\n%s", output)
		}
	})
}

func TestTestSymbolExists(t *testing.T) {
	src := `package thing

type TestShape struct{}

func TestReal(t *testing.T) {}

func helper() {}
`
	tests := []struct {
		symbol string
		want   bool
	}{
		{"TestReal", true},
		{"helper", true},
		{"TestShape", false}, // a type is not a test function
		{"Missing", false},
	}
	for _, tt := range tests {
		if got := testSymbolExists(src, tt.symbol); got != tt.want {
			t.Errorf("testSymbolExists(%q) = %v, want %v", tt.symbol, got, tt.want)
		}
	}
}

func TestSymbolExists(t *testing.T) {
	src := `package thing

type Doer interface {
	Do() error
}

type Thing struct{}

const Limit = 3

var registry = map[string]int{}

func Do() {}

func (t *Thing) Run() error { return nil }

func (t *Thing) hidden() {}
`
	tests := []struct {
		symbol string
		want   bool
	}{
		{"Do", true},
		{"Doer", true},
		{"Thing", true},
		{"Limit", true},
		{"registry", true},
		{"Thing.Run", true},
		{"Thing.hidden", true},
		{"Missing", false},
		{"Thing.Missing", false},
	}
	for _, tt := range tests {
		if got := symbolExists(src, tt.symbol); got != tt.want {
			t.Errorf("symbolExists(%q) = %v, want %v", tt.symbol, got, tt.want)
		}
	}
}

// loadFixtureCatalog reads a one-rule catalog fixture directory
// (testdata/catalog/<name>/<module>/<ID>.md) through the real directory loader.
func loadFixtureCatalog(t *testing.T, name string) Catalog {
	t.Helper()

	catalog, err := loadCatalog(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("load fixture catalog %s: %v", name, err)
	}
	return catalog
}

// TestCheckCategoryBinding is policy 14: the category binds a rule's refusal
// surface and its proof. `interface` names the status it refuses with and is
// never proved by a scenario; `refusal-mapping` and `persistence-fact` are
// proved by Go tests; `invariant`/`policy` want a scenario (a WARN, never a
// failure); `orchestration`/`boundary` are free; specified-only is exempt.
func TestCheckCategoryBinding(t *testing.T) {
	tests := []struct {
		name         string
		fixture      string
		mutate       func(*Rule)
		wantErrors   int
		wantWarnings int
		wantMessage  string
	}{
		{
			name:       "interface without a refusal",
			fixture:    "catalog/policy14-interface-no-refusal",
			wantErrors: 1, wantMessage: "no refusal",
		},
		{
			name: "interface refusal without an http status",
			mutate: func(r *Rule) {
				r.Category = "interface"
				r.Authority = "handler"
				r.Refusal = &Refusal{Code: "synthetic"}
			},
			wantErrors: 1, wantMessage: "no http status",
		},
		{
			name: "interface carrying a gherkin pointer",
			mutate: func(r *Rule) {
				r.Category = "interface"
				r.Authority = "handler"
				r.Refusal = &Refusal{Code: "synthetic", HTTP: ptr(422)}
				r.Verification.Gherkin = []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"}
			},
			wantErrors: 1, wantMessage: "gherkin pointer",
		},
		{
			name: "interface complete",
			mutate: func(r *Rule) {
				r.Category = "interface"
				r.Authority = "handler"
				r.Refusal = &Refusal{Code: "synthetic", HTTP: ptr(422)}
			},
		},
		{
			name: "refusal-mapping carrying a gherkin pointer",
			mutate: func(r *Rule) {
				r.Category = "refusal-mapping"
				r.Authority = "repository"
				r.Refusal = &Refusal{Code: "P0001", HTTP: ptr(409)}
				r.Verification.Gherkin = []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"}
			},
			wantErrors: 1, wantMessage: "gherkin pointer",
		},
		{
			name:       "persistence-fact without a unit pointer",
			fixture:    "catalog/policy14-persistence-fact-no-unit",
			wantErrors: 1, wantMessage: "no unit verification pointer",
		},
		{
			name: "persistence-fact carrying a gherkin pointer",
			mutate: func(r *Rule) {
				r.Category = "persistence-fact"
				r.Authority = "repository"
				r.Verification.Gherkin = []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"}
			},
			wantErrors: 1, wantMessage: "gherkin pointer",
		},
		{
			name: "persistence-fact complete",
			mutate: func(r *Rule) {
				r.Category = "persistence-fact"
				r.Authority = "repository"
			},
		},
		{
			name: "invariant without a gherkin pointer warns",
			mutate: func(r *Rule) {
				r.Category = "invariant"
				r.Authority = "database"
				r.Mechanism = ptr("constraint")
				r.WriteScope = ptr("row-local")
			},
			wantWarnings: 1, wantMessage: "no gherkin pointer",
		},
		{
			name: "policy without a gherkin pointer warns",
			mutate: func(r *Rule) {
				r.Category = "policy"
				r.Authority = "domain"
			},
			wantWarnings: 1, wantMessage: "no gherkin pointer",
		},
		{
			name: "invariant with a gherkin pointer is silent",
			mutate: func(r *Rule) {
				r.Category = "invariant"
				r.Authority = "database"
				r.Mechanism = ptr("constraint")
				r.WriteScope = ptr("row-local")
				r.Verification.Gherkin = []string{"docs/spec/features/kernel/thing.feature::A thing always is a thing"}
			},
		},
		{
			name: "orchestration is unconstrained",
			mutate: func(r *Rule) {
				r.Category = "orchestration"
				r.Authority = "service"
			},
		},
		{
			name: "boundary is unconstrained",
			mutate: func(r *Rule) {
				r.Category = "boundary"
				r.Authority = "composition-root"
			},
		},
		{
			name: "specified-only is exempt",
			mutate: func(r *Rule) {
				r.Status = statusSpecifiedOnly
				r.Category = "interface"
				r.Authority = "handler"
				r.Enforcement = nil
				r.Verification = Verification{}
				r.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)

			var catalog Catalog
			if tt.fixture != "" {
				catalog = loadFixtureCatalog(t, tt.fixture)
			} else {
				rule := baseRule()
				tt.mutate(&rule)
				catalog = Catalog{rule}
			}

			findings := collectFindings(catalog, repo.root)
			errorsFound, warnings := countFindings(findings)
			if errorsFound != tt.wantErrors || warnings != tt.wantWarnings {
				t.Fatalf("got %d errors / %d warnings, want %d / %d:\n%s",
					errorsFound, warnings, tt.wantErrors, tt.wantWarnings, describeFindings(findings))
			}
			if tt.wantMessage == "" {
				return
			}
			group := findingsFor(findings, policyCategory)
			if len(group) != tt.wantErrors+tt.wantWarnings {
				t.Fatalf("policy %s carried %d findings, want %d:\n%s",
					policyCategory, len(group), tt.wantErrors+tt.wantWarnings, describeFindings(findings))
			}
			if !strings.Contains(group[0].Message, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", group[0].Message, tt.wantMessage)
			}
		})
	}
}

// TestCheckKnowledgeRef is policy 16: knowledge-ref is a path relative to the
// repository root, it exists, and it sits under the learning-phase tree.
func TestCheckKnowledgeRef(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		mutate      func(*Rule)
		wantErrors  int
		wantMessage string
	}{
		{name: "absent is allowed"},
		{
			name: "an existing learning-phase note",
			mutate: func(r *Rule) {
				r.KnowledgeRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
			},
		},
		{
			name:       "outside the learning-phase tree",
			fixture:    "catalog/policy16-knowledge-ref-outside",
			wantErrors: 1, wantMessage: "is not under docs/league-tokens-learning-phases/",
		},
		{
			name:       "a note that does not exist",
			fixture:    "catalog/policy16-knowledge-ref-missing",
			wantErrors: 1, wantMessage: "does not exist",
		},
		{
			name:       "an absolute path",
			mutate:     func(r *Rule) { r.KnowledgeRef = ptr("/etc/hosts") },
			wantErrors: 1, wantMessage: "is absolute",
		},
		{
			name: "a traversal out of the tree",
			mutate: func(r *Rule) {
				r.KnowledgeRef = ptr("docs/league-tokens-learning-phases/../README.md")
			},
			wantErrors: 1, wantMessage: "is not under docs/league-tokens-learning-phases/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)

			var catalog Catalog
			if tt.fixture != "" {
				catalog = loadFixtureCatalog(t, tt.fixture)
			} else {
				rule := baseRule()
				if tt.mutate != nil {
					tt.mutate(&rule)
				}
				catalog = Catalog{rule}
			}

			findings := collectFindings(catalog, repo.root)
			errorsFound, warnings := countFindings(findings)
			if errorsFound != tt.wantErrors || warnings != 0 {
				t.Fatalf("got %d errors / %d warnings, want %d / 0:\n%s",
					errorsFound, warnings, tt.wantErrors, describeFindings(findings))
			}
			if tt.wantMessage == "" {
				return
			}
			group := findingsFor(findings, policyKnowledgeRef)
			if len(group) != tt.wantErrors {
				t.Fatalf("policy %s carried %d findings, want %d:\n%s",
					policyKnowledgeRef, len(group), tt.wantErrors, describeFindings(findings))
			}
			if !strings.Contains(group[0].Message, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", group[0].Message, tt.wantMessage)
			}
		})
	}
}

// TestKnowledgeRefIsAdditionalToExpertRef pins the field's shape: a
// specified-only rule keeps expert-ref and adds knowledge-ref beside it.
func TestKnowledgeRefIsAdditionalToExpertRef(t *testing.T) {
	repo := newTestRepo(t)
	catalog := loadFixtureCatalog(t, "catalog/policy16-specified-only")
	if len(catalog) != 1 {
		t.Fatalf("fixture carries %d rules, want 1", len(catalog))
	}

	rule := catalog[0]
	const note = "docs/league-tokens-learning-phases/phase-03.md"
	if rule.ExpertRef == nil || *rule.ExpertRef != note {
		t.Errorf("expert-ref = %v, want %q", rule.ExpertRef, note)
	}
	if rule.KnowledgeRef == nil || *rule.KnowledgeRef != note {
		t.Errorf("knowledge-ref = %v, want %q", rule.KnowledgeRef, note)
	}

	if findings := collectFindings(catalog, repo.root); len(findings) != 0 {
		t.Fatalf("specified-only rule with both refs produced findings:\n%s", describeFindings(findings))
	}
}
