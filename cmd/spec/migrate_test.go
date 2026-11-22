package main

// Tests for the per-rule-file catalog format and the `spec migrate` command
// (migrate.go). The fixture (testdata/legacy-rules.yaml) is a synthetic
// OLD-format catalog: the suite never reads docs/spec/rules.yaml, because the
// real 126-rule round trip runs inside the migrate command itself and a test
// that depended on the tree being migrated would have to change with it.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const legacyFixturePath = "testdata/legacy-rules.yaml"

// loadLegacyFixture decodes the synthetic old-format catalog with the real
// single-file legacy loader, so the fixture also pins that `spec migrate` still
// understands the format it reads.
func loadLegacyFixture(t *testing.T) legacyCatalog {
	t.Helper()
	catalog, err := loadLegacyCatalog(legacyFixturePath)
	if err != nil {
		t.Fatalf("load legacy fixture: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatalf("legacy fixture decoded to zero rules")
	}
	return catalog
}

// fixtureRule transforms one fixture rule and renders its file.
func fixtureRule(t *testing.T, id string) (Rule, []byte) {
	t.Helper()
	for _, old := range loadLegacyFixture(t) {
		if old.ID != id {
			continue
		}
		rule := transformRule(old)
		file, err := renderRuleFile(rule)
		if err != nil {
			t.Fatalf("%s: render rule file: %v", id, err)
		}
		return rule, file
	}
	t.Fatalf("legacy fixture has no rule %s", id)
	return Rule{}, nil
}

// TestMigrateFixtureRoundTrip is the format's contract: old → transform → render
// → parse gives back the transformed rule, and rendering the reparsed rule is
// byte-identical (so a migrated tree re-renders to itself).
func TestMigrateFixtureRoundTrip(t *testing.T) {
	catalog := loadLegacyFixture(t)
	statuses := make(map[string]bool)

	for _, old := range catalog {
		want := transformRule(old)
		file, err := renderRuleFile(want)
		if err != nil {
			t.Fatalf("%s: render rule file: %v", want.ID, err)
		}

		if got := strings.Count(string(file), "\n"+ruleFileFence+"\n"); got != 1 {
			t.Errorf("%s: expected exactly one closing front-matter fence, found %d:\n%s",
				want.ID, got, file)
		}
		if !strings.HasPrefix(string(file), ruleFileFence+"\n") {
			t.Errorf("%s: file does not start with the %q fence:\n%s", want.ID, ruleFileFence, file)
		}
		if !strings.HasSuffix(string(file), "\n") || strings.HasSuffix(string(file), "\n\n") {
			t.Errorf("%s: file does not end with exactly one newline: %q", want.ID, string(file))
		}

		got, err := parseRuleFile(file, want.ID+".md")
		if err != nil {
			t.Fatalf("%s: parse the rendered file: %v\n%s", want.ID, err, file)
		}
		if diff := ruleDiff(want, got); len(diff) > 0 {
			t.Errorf("%s: round trip lost field(s): %s", want.ID, strings.Join(diff, ", "))
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: round trip is not DeepEqual:\nwant %#v\ngot  %#v", want.ID, want, got)
		}

		again, err := renderRuleFile(got)
		if err != nil {
			t.Fatalf("%s: re-render: %v", want.ID, err)
		}
		if !bytes.Equal(file, again) {
			t.Errorf("%s: render is not stable across a parse:\nfirst:\n%s\nsecond:\n%s",
				want.ID, file, again)
		}

		statuses[want.Status] = true
	}

	for _, status := range []string{statusImplemented, statusSpecifiedOnly, statusDrifted} {
		if !statuses[status] {
			t.Errorf("fixture does not cover status %q", status)
		}
	}
}

// TestTransformLegacyFixture pins the extraction on the fixture: the exact field
// values the documented marker rules produce, and the front-matter shape
// (write_scope, no notes field, body only when there is residual prose).
func TestTransformLegacyFixture(t *testing.T) {
	cases := []struct {
		id           string
		writeScope   *string
		stalePointer *string
		tie          *string
		notes        string
	}{
		{
			id:         "R-KERNEL-001",
			writeScope: ptr("row-local"),
			tie:        ptr("the copy fails loudly — a key the validator accepts but the table refuses makes the command's transaction fail."),
			notes: "The validator is the authority and the table's CHECK is its path-independent " +
				"backstop. Coverage gap: nothing asserts the 255-byte bound through a direct write.",
		},
		{
			id:           "R-KERNEL-002",
			writeScope:   nil,
			stalePointer: ptr("the comment names a symbol that no longer exists. Second finding: the same comment names a path the refactor deleted."),
			notes:        "",
		},
		{
			id:           "R-GAME-001",
			writeScope:   ptr("cross-row"),
			stalePointer: ptr("the migration header still names the handler the refactor deleted."),
			tie:          ptr("the copies fail loudly — TestGameGate pins a refused arrival), and the gate cannot vanish."),
			notes: "Authority is the service, deviating from the invariant default: the verdict " +
				"belongs to the use case.",
		},
		{
			id:         "R-GAME-002",
			writeScope: nil,
			tie: ptr("dropping the guard lets a stale claim through. the read's refusal keeps " +
				"the field shape, so a client never sees an existence bit."),
			notes: "The domain is the only writer. The read path is advisory.",
		},
		{
			id:         "R-IDENTITY-001",
			writeScope: ptr("row-local"),
			notes:      "",
		},
	}

	catalog := loadLegacyFixture(t)
	for _, tc := range cases {
		var old legacyRule
		found := false
		for _, candidate := range catalog {
			if candidate.ID == tc.id {
				old, found = candidate, true
				break
			}
		}
		if !found {
			t.Fatalf("fixture has no rule %s", tc.id)
		}

		got := transformRule(old)
		if !reflect.DeepEqual(got.WriteScope, tc.writeScope) {
			t.Errorf("%s: write_scope = %v, want %v", tc.id, deref(got.WriteScope), deref(tc.writeScope))
		}
		if !reflect.DeepEqual(got.StalePointer, tc.stalePointer) {
			t.Errorf("%s: stale_pointer = %q, want %q", tc.id, deref(got.StalePointer), deref(tc.stalePointer))
		}
		if !reflect.DeepEqual(got.Tie, tc.tie) {
			t.Errorf("%s: tie = %q, want %q", tc.id, deref(got.Tie), deref(tc.tie))
		}
		if got.Notes != tc.notes {
			t.Errorf("%s: notes = %q, want %q", tc.id, got.Notes, tc.notes)
		}

		file := string(mustRender(t, got))
		if strings.Contains(file, "\nscope:") || strings.Contains(file, "\n  scope:") {
			t.Errorf("%s: rendered file still carries the old `scope:` field:\n%s", tc.id, file)
		}
		if !strings.Contains(file, "write_scope:") {
			t.Errorf("%s: rendered file has no `write_scope:` field:\n%s", tc.id, file)
		}
		if strings.Contains(file, "\nnotes:") {
			t.Errorf("%s: rendered file carries notes as a front-matter field:\n%s", tc.id, file)
		}
		switch {
		case tc.notes == "" && strings.Contains(file, ruleNotesHeading):
			t.Errorf("%s: rendered file has a Notes body but no residual prose:\n%s", tc.id, file)
		case tc.notes != "" && !strings.Contains(file, "\n"+ruleFileFence+"\n\n"+ruleNotesHeading+"\n\n"+tc.notes+"\n"):
			t.Errorf("%s: rendered file's Notes body is not the canonical residual prose:\n%s", tc.id, file)
		}
	}
}

// TestSplitNotes pins the documented extraction rules independently of the
// fixture: sentence-scoped Tie, segment-scoped stale-pointer, multiple markers
// joined in order, and the residual.
func TestSplitNotes(t *testing.T) {
	cases := []struct {
		name         string
		notes        string
		stalePointer *string
		tie          *string
		residual     string
	}{
		{
			name:     "no markers",
			notes:    "Only prose, no markers.",
			residual: "Only prose, no markers.",
		},
		{
			name:     "tie is one sentence, later prose stays residual",
			notes:    "Intro prose. Tie: the copy fails loudly — TestX pins it. Trailing coverage gap.",
			tie:      ptr("the copy fails loudly — TestX pins it."),
			residual: "Intro prose. Trailing coverage gap.",
		},
		{
			name:         "stale-pointer takes the rest of the notes",
			notes:        "Intro. stale-pointer: one finding. (1) The detail. Second finding: more.",
			stalePointer: ptr("one finding. (1) The detail. Second finding: more."),
			residual:     "Intro.",
		},
		{
			name:         "stale-pointer alone leaves no residual",
			notes:        "stale-pointer: only this.",
			stalePointer: ptr("only this."),
			residual:     "",
		},
		{
			name:         "marker inside a parenthetical",
			notes:        "Intro. (Tie: copies fail loudly), and the gate cannot vanish. stale-pointer: the header is stale.",
			stalePointer: ptr("the header is stale."),
			tie:          ptr("copies fail loudly), and the gate cannot vanish."),
			residual:     "Intro.",
		},
		{
			name:     "two ties are joined in document order",
			notes:    "A. Tie: one tie. B. Tie: two tie.",
			tie:      ptr("one tie. two tie."),
			residual: "A. B.",
		},
		{
			name:     "line wrapping and repeated spaces do not matter",
			notes:    "  Intro   prose.\n\n Tie: the copy\n fails loudly.  ",
			tie:      ptr("the copy fails loudly."),
			residual: "Intro prose.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stalePointer, tie, residual := splitNotes(tc.notes)
			if deref(stalePointer) != deref(tc.stalePointer) {
				t.Errorf("stale_pointer = %q, want %q", deref(stalePointer), deref(tc.stalePointer))
			}
			if (stalePointer == nil) != (tc.stalePointer == nil) {
				t.Errorf("stale_pointer presence = %v, want %v", stalePointer != nil, tc.stalePointer != nil)
			}
			if deref(tie) != deref(tc.tie) {
				t.Errorf("tie = %q, want %q", deref(tie), deref(tc.tie))
			}
			if (tie == nil) != (tc.tie == nil) {
				t.Errorf("tie presence = %v, want %v", tie != nil, tc.tie != nil)
			}
			if residual != tc.residual {
				t.Errorf("residual = %q, want %q", residual, tc.residual)
			}
		})
	}
}

// TestParseRuleFileRejectsUnknownFrontMatterField pins strictness: a misspelled
// key would otherwise be dropped silently, and a dropped `stale_pointer` or `tie`
// is invisible to every policy downstream.
func TestParseRuleFileRejectsUnknownFrontMatterField(t *testing.T) {
	_, file := fixtureRule(t, "R-KERNEL-001")
	mutated := strings.Replace(string(file), "id: R-KERNEL-001\n",
		"id: R-KERNEL-001\nbogus_field: 1\n", 1)
	if mutated == string(file) {
		t.Fatalf("fixture rule file was not mutated; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "bogus_field", "not found in type")
}

// TestParseRuleFileRejectsLegacyScopeKey is the rename's guard: `scope:` is the
// old field name and must be a hard error, not a silently ignored key.
func TestParseRuleFileRejectsLegacyScopeKey(t *testing.T) {
	_, file := fixtureRule(t, "R-KERNEL-001")
	mutated := strings.Replace(string(file), "write_scope: row-local", "scope: row-local", 1)
	if mutated == string(file) {
		t.Fatalf("fixture rule file was not mutated; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "scope", "not found in type")
}

// TestParseRuleFileRejectsNotesField pins that notes died as a front-matter
// field: the prose belongs in the body, and a `notes:` key is a typo the strict
// decoder must catch.
func TestParseRuleFileRejectsNotesField(t *testing.T) {
	_, file := fixtureRule(t, "R-KERNEL-001")
	mutated := strings.Replace(string(file), "stale_pointer: null\n",
		"stale_pointer: null\nnotes: handwritten prose\n", 1)
	if mutated == string(file) {
		t.Fatalf("fixture rule file was not mutated; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "notes", "not found in type")
}

// TestParseRuleFileRejectsBodyWithoutNotesHeading: body prose the loader cannot
// attribute would be invisible to the policies, so it is an error rather than a
// silent drop.
func TestParseRuleFileRejectsBodyWithoutNotesHeading(t *testing.T) {
	_, file := fixtureRule(t, "R-IDENTITY-001")
	mutated := string(file) + "\nLoose prose with no heading.\n"
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, ruleNotesHeading)
}

// TestParseRuleFileRequiresFences: a file without the front-matter fences is not
// a rule file, and neither is one whose fence never closes.
func TestParseRuleFileRequiresFences(t *testing.T) {
	if _, err := parseRuleFile([]byte("id: R-KERNEL-001\n"), "unfenced.md"); err == nil {
		t.Error("parseRuleFile accepted a file with no front-matter fence")
	}
	if _, err := parseRuleFile([]byte("---\nid: R-KERNEL-001\n"), "unclosed.md"); err == nil {
		t.Error("parseRuleFile accepted a file with no closing fence")
	}
}

// TestParseRuleFileNormalizesBodyProse pins the canonical Notes form: the body is
// prose, so its line wrapping can never change the decoded Notes string.
func TestParseRuleFileNormalizesBodyProse(t *testing.T) {
	_, file := fixtureRule(t, "R-IDENTITY-001")
	withBody := string(file) + "\n" + ruleNotesHeading + "\n\n  wrapped   prose\n  over two lines\n"
	rule, err := parseRuleFile([]byte(withBody), "wrapped.md")
	if err != nil {
		t.Fatalf("parse rule file with a wrapped body: %v", err)
	}
	if want := "wrapped prose over two lines"; rule.Notes != want {
		t.Errorf("Notes = %q, want %q", rule.Notes, want)
	}
}

// TestRunMigrateWritesPerRuleFiles exercises the command end to end on the
// fixture: one file per rule under <out>/<module>/<ID>.md, the per-module summary,
// and the --force guard on a non-empty target.
func TestRunMigrateWritesPerRuleFiles(t *testing.T) {
	catalog := loadLegacyFixture(t)
	out := filepath.Join(t.TempDir(), "rules")

	var stdout, stderr bytes.Buffer
	if code := runMigrate([]string{"--rules", legacyFixturePath, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("runMigrate exited %d: %s", code, stderr.String())
	}
	total := 0
	for _, old := range catalog {
		path := filepath.Join(out, old.Module, old.ID+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to be written: %v", path, err)
			continue
		}
		total++
	}
	if total != len(catalog) {
		t.Errorf("wrote %d rule files, want %d", total, len(catalog))
	}
	wantLine := "5/5 round-trip ok"
	if !strings.Contains(stdout.String(), wantLine) {
		t.Errorf("stdout does not report %q:\n%s", wantLine, stdout.String())
	}
	for _, module := range []string{"game", "identity", "kernel"} {
		if !strings.Contains(stdout.String(), module) {
			t.Errorf("stdout has no summary line for module %q:\n%s", module, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := runMigrate([]string{"--rules", legacyFixturePath, "--out", out}, &stdout, &stderr); code != 1 {
		t.Fatalf("runMigrate into a non-empty target exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("refusal does not mention --force: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runMigrate([]string{"--rules", legacyFixturePath, "--out", out, "--force"},
		&stdout, &stderr); code != 0 {
		t.Fatalf("runMigrate --force exited %d: %s", code, stderr.String())
	}
}

// TestRuleDiffNamesDifferingFields pins the round-trip gate's report: it must
// name the field, not dump the struct.
func TestRuleDiffNamesDifferingFields(t *testing.T) {
	rule, _ := fixtureRule(t, "R-KERNEL-001")
	changed := rule
	changed.Tie = ptr("something else")
	changed.Notes = "different prose"

	diff := ruleDiff(rule, changed)
	if want := []string{"tie", "notes"}; !reflect.DeepEqual(diff, want) {
		t.Fatalf("ruleDiff = %v, want %v", diff, want)
	}
	if diff := ruleDiff(rule, rule); len(diff) != 0 {
		t.Fatalf("ruleDiff of identical rules = %v, want none", diff)
	}
}

func mustRender(t *testing.T, rule Rule) []byte {
	t.Helper()
	file, err := renderRuleFile(rule)
	if err != nil {
		t.Fatalf("%s: render rule file: %v", rule.ID, err)
	}
	return file
}

// assertParseError requires a non-nil error mentioning every wanted fragment.
func assertParseError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("parse accepted an invalid rule file")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// deref renders a *string for an error message.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
