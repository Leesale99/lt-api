package main

// rulefile_test.go pins both halves of the rule-file format: the ONE parser
// (catalog.go parseRuleFile) and its exact inverse, the canonical writer
// (catalog.go renderRuleFile).
//
// The load-bearing test is TestRuleFileRoundTripLiveCatalog. It walks the real
// catalog and proves, for every rule file, that render(parse(file)) is
// byte-identical to the bytes on disk and that parsing the re-render gives back
// the same Rule. That is the lasting successor of the one-shot `spec migrate`
// round-trip gate: the gate held only on the day the tree was converted, this
// holds on every run.
//
// The rest of the file keeps the pins that outlived the migration: front-matter
// strictness (an unknown key, the retired `scope:`, a `notes:` key, a second YAML
// document), body strictness (prose outside `## Notes`, canonical normalization),
// the writer's shape, and the field-naming diff used to report a round-trip loss.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// liveCatalogDir is the canonical catalog, relative to this package. The test
// reads the tree as authored rather than writing fixtures: a format the writer
// cannot reproduce byte-for-byte is exactly what it must catch.
const liveCatalogDir = "../../docs/spec/rules"

// liveCatalogRuleCount is the catalog's authored size. It is asserted so a test
// that silently stops finding files (a moved directory, a changed layout) fails
// instead of passing vacuously.
const liveCatalogRuleCount = 126

// TestRuleFileRoundTripLiveCatalog is the format's contract over the live
// catalog: parse → render → parse, with the render byte-identical to the file on
// disk and the second parse DeepEqual to the first.
func TestRuleFileRoundTripLiveCatalog(t *testing.T) {
	files := liveRuleFiles(t)
	if len(files) != liveCatalogRuleCount {
		t.Fatalf("found %d rule files under %s, want %d", len(files), liveCatalogDir, liveCatalogRuleCount)
	}
	for _, rel := range files {
		t.Run(strings.TrimSuffix(rel, ".md"), func(t *testing.T) {
			path := filepath.Join(liveCatalogDir, filepath.FromSlash(rel))
			onDisk, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			rule, err := parseRuleFile(onDisk, rel)
			if err != nil {
				t.Fatalf("parse the file on disk: %v", err)
			}

			rendered, err := renderRuleFile(rule)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !bytes.Equal(onDisk, rendered) {
				t.Errorf("the canonical writer does not reproduce the file on disk:\n%s",
					firstLineDiff(onDisk, rendered))
			}

			reparsed, err := parseRuleFile(rendered, rel)
			if err != nil {
				t.Fatalf("parse the re-rendered file: %v", err)
			}
			if diff := ruleDiff(rule, reparsed); len(diff) > 0 {
				t.Errorf("round trip lost field(s): %s", strings.Join(diff, ", "))
			}
			if !reflect.DeepEqual(rule, reparsed) {
				t.Errorf("round trip is not DeepEqual:\nwant %#v\ngot  %#v", rule, reparsed)
			}
		})
	}
}

// liveRuleFiles lists the catalog's rule files as slash-relative
// `<module>/<ID>.md` paths, skipping the rules directory's authored README (the
// one non-rule file the loader allows). The order is readdir order, which the
// catalog loader is required not to depend on.
func liveRuleFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(liveCatalogDir)
	if err != nil {
		t.Fatalf("read %s: %v", liveCatalogDir, err)
	}
	var files []string
	for _, module := range entries {
		if !module.IsDir() {
			if module.Name() == catalogReadmeFile {
				continue
			}
			t.Errorf("%s holds the non-directory %q; only %s is allowed beside the module "+
				"directories", liveCatalogDir, module.Name(), catalogReadmeFile)
			continue
		}
		moduleDir := filepath.Join(liveCatalogDir, module.Name())
		rules, err := os.ReadDir(moduleDir)
		if err != nil {
			t.Fatalf("read %s: %v", moduleDir, err)
		}
		for _, rule := range rules {
			if filepath.Ext(rule.Name()) != ".md" {
				continue
			}
			files = append(files, module.Name()+"/"+rule.Name())
		}
	}
	return files
}

// TestRenderRuleFileShape pins the writer's shape for a rule that is not on disk
// yet (a new rule file): the fences, exactly one closing fence, exactly one
// trailing newline, and a `## Notes` body only when the rule carries residual
// prose. The live round trip above cannot cover this — it only ever sees files
// the writer already produced.
func TestRenderRuleFileShape(t *testing.T) {
	withNotes := baseRule()
	withNotes.Notes = "Author prose for the body."

	for _, tc := range []struct {
		name  string
		rule  Rule
		notes bool
	}{
		{name: "front matter only", rule: baseRule()},
		{name: "with a notes body", rule: withNotes, notes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := renderedRule(t, tc.rule)
			if !strings.HasPrefix(file, ruleFileFence+"\n") {
				t.Errorf("file does not start with the %q fence:\n%s", ruleFileFence, file)
			}
			if got := strings.Count(file, "\n"+ruleFileFence+"\n"); got != 1 {
				t.Errorf("expected exactly one closing front-matter fence, found %d:\n%s", got, file)
			}
			if !strings.HasSuffix(file, "\n") || strings.HasSuffix(file, "\n\n") {
				t.Errorf("file does not end with exactly one newline: %q", file)
			}
			hasBody := strings.Contains(file, "\n"+ruleFileFence+"\n\n"+ruleNotesHeading+"\n")
			if hasBody != tc.notes {
				t.Errorf("has a %q body = %v, want %v:\n%s",
					ruleNotesHeading, hasBody, tc.notes, file)
			}
			if tc.notes && !strings.Contains(file, "\n\n"+ruleNotesHeading+"\n\n"+tc.rule.Notes+"\n") {
				t.Errorf("the body is not the rule's canonical prose:\n%s", file)
			}
			// The rename guard, at the writer: the retired `scope:` name and the
			// retired `notes:` front-matter key can never come back.
			if strings.Contains(file, "\nscope:") || strings.Contains(file, "\n  scope:") {
				t.Errorf("rendered file carries the retired `scope:` field:\n%s", file)
			}
			if !strings.Contains(file, "write_scope:") {
				t.Errorf("rendered file has no `write_scope:` field:\n%s", file)
			}
			if strings.Contains(file, "\nnotes:") {
				t.Errorf("rendered file carries notes as a front-matter field:\n%s", file)
			}
		})
	}
}

// TestParseRuleFileRejectsUnknownFrontMatterField pins strictness: a misspelled
// key would otherwise be dropped silently, and a dropped `stale_pointer` or `tie`
// is invisible to every policy downstream.
func TestParseRuleFileRejectsUnknownFrontMatterField(t *testing.T) {
	file := renderedRule(t, baseRule())
	mutated := strings.Replace(file, "id: R-KERNEL-001\n", "id: R-KERNEL-001\nbogus_field: 1\n", 1)
	if mutated == file {
		t.Fatalf("the fixture rule file was not mutated; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "bogus_field", "not found in type")
}

// TestParseRuleFileRejectsLegacyScopeKey is the rename's guard: `scope:` is the
// old field name and must be a hard error, not a silently ignored key.
func TestParseRuleFileRejectsLegacyScopeKey(t *testing.T) {
	rule := baseRule()
	rule.WriteScope = ptr("row-local")
	file := renderedRule(t, rule)
	mutated := strings.Replace(file, "write_scope: row-local", "scope: row-local", 1)
	if mutated == file {
		t.Fatalf("write_scope: row-local was not in the fixture; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "scope", "not found in type")
}

// TestParseRuleFileRejectsNotesField pins that notes died as a front-matter
// field: the prose belongs in the body, and a `notes:` key is a typo the strict
// decoder must catch.
func TestParseRuleFileRejectsNotesField(t *testing.T) {
	file := renderedRule(t, baseRule())
	mutated := strings.Replace(file, "stale_pointer: null\n",
		"stale_pointer: null\nnotes: handwritten prose\n", 1)
	if mutated == file {
		t.Fatalf("stale_pointer: null was not in the fixture; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "notes", "not found in type")
}

// TestParseRuleFileRejectsSecondFrontMatterDocument pins the one-document rule: a
// stray `...` document-end marker inside the front matter must be an error rather
// than a silently dropped second document. (A `---` line cannot reach the
// decoder — it closes the fence first — so `...` is how the guard is exercised.)
func TestParseRuleFileRejectsSecondFrontMatterDocument(t *testing.T) {
	file := renderedRule(t, baseRule())
	mutated := strings.Replace(file, "id: R-KERNEL-001\n", "id: R-KERNEL-001\n...\n", 1)
	if mutated == file {
		t.Fatalf("the fixture rule file was not mutated; the test would pass vacuously")
	}
	_, err := parseRuleFile([]byte(mutated), "mutated.md")
	assertParseError(t, err, "exactly one YAML document")
}

// TestParseRuleFileRejectsBodyWithoutNotesHeading: body prose the loader cannot
// attribute would be invisible to the policies, so it is an error rather than a
// silent drop.
func TestParseRuleFileRejectsBodyWithoutNotesHeading(t *testing.T) {
	file := renderedRule(t, baseRule()) // no residual prose, so no body
	if strings.Contains(file, ruleNotesHeading) {
		t.Fatalf("the fixture already has a %q body:\n%s", ruleNotesHeading, file)
	}
	_, err := parseRuleFile([]byte(file+"\nLoose prose with no heading.\n"), "mutated.md")
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
	file := renderedRule(t, baseRule())
	withBody := file + "\n" + ruleNotesHeading + "\n\n  wrapped   prose\n  over two lines\n"
	rule, err := parseRuleFile([]byte(withBody), "wrapped.md")
	if err != nil {
		t.Fatalf("parse rule file with a wrapped body: %v", err)
	}
	if want := "wrapped prose over two lines"; rule.Notes != want {
		t.Errorf("Notes = %q, want %q", rule.Notes, want)
	}
}

// TestRuleDiffNamesDifferingFields pins the round-trip report: it must name the
// field, not dump the struct, so a failure in the live-catalog test above reads
// as "R-GAME-001: tie" rather than a wall of values.
func TestRuleDiffNamesDifferingFields(t *testing.T) {
	rule := baseRule()
	rule.Tie = ptr("a copy tied to its authority.")
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

// ruleDiff names the Rule fields whose values differ, so a round-trip failure
// reports "R-GAME-001 failed round-trip in field(s): tie" instead of a wall of
// struct dumps. It is a test helper: the only consumer is the round-trip test.
func ruleDiff(want, got Rule) []string {
	var diff []string
	wantValue, gotValue := reflect.ValueOf(want), reflect.ValueOf(got)
	typ := wantValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if reflect.DeepEqual(wantValue.Field(i).Interface(), gotValue.Field(i).Interface()) {
			continue
		}
		name := field.Tag.Get("yaml")
		if name == "" || name == "-" {
			name = strings.ToLower(field.Name)
		}
		diff = append(diff, name)
	}
	return diff
}

// firstLineDiff reports the first differing line of two rule files (1-based), so
// a byte-identity failure points at the field that moved instead of dumping both
// documents.
func firstLineDiff(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return "first difference at line " + strconv.Itoa(i+1) +
				"\non disk: " + w + "\nrendered: " + g
		}
	}
	return "the files differ only in their trailing bytes"
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
