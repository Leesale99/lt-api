package main

// Red-first pin for the refusal policy's HTTP range: `refusal.http: 0` and
// `refusal.http: 999` are not statuses a handler can write, but the `refusal`
// policy accepted them (only the SQLSTATE was checked). Fixtures live under
// testdata/catalog, like the other policy fixtures.

import (
	"strconv"
	"strings"
	"testing"
)

// TestPinnedRefusalHTTPRange is fix E: `refusal.http` must be a real HTTP
// status (100–599). Out-of-range values are an ERROR under the existing
// `refusal` policy; the boundaries stay accepted.
func TestPinnedRefusalHTTPRange(t *testing.T) {
	rejected := []struct {
		name    string
		fixture string
		http    int
	}{
		{"zero", "catalog/refusal-http-zero", 0},
		{"nine hundred ninety nine", "catalog/refusal-http-999", 999},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			findings := collectFindings(loadFixtureCatalog(t, tt.fixture), repo.root)

			group := findingsFor(findings, "17-refusal")
			if len(group) != 1 {
				t.Fatalf("%d refusal findings, want 1:\n%s", len(group), describeFindings(findings))
			}
			if group[0].Severity != severityError {
				t.Errorf("refusal http %d is a %v, want an error", tt.http, group[0].Severity)
			}
			for _, want := range []string{"outside the HTTP status range", strconv.Itoa(tt.http)} {
				if !strings.Contains(group[0].Message, want) {
					t.Errorf("message %q does not mention %q", group[0].Message, want)
				}
			}
		})
	}

	for _, http := range []int{100, 404, 599} {
		t.Run("accepts "+strconv.Itoa(http), func(t *testing.T) {
			repo := newTestRepo(t)
			rule := baseRule()
			rule.Refusal = &Refusal{Code: "synthetic", HTTP: ptr(http)}
			findings := collectFindings(Catalog{rule}, repo.root)
			if group := findingsFor(findings, "17-refusal"); len(group) != 0 {
				t.Fatalf("refusal http %d produced findings:\n%s", http, describeFindings(group))
			}
		})
	}
}
