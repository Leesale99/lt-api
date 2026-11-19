package spec

// The known-defect announcer (style guide §9): the default run excludes every
// @known-defect scenario, and this note is what keeps that exclusion honest — a
// green run says, in the open, which behaviours it did not check.
//
// The notes are read from the feature files themselves through the gherkin
// parser (the same parse godog performs), so a tag and its note can never
// disagree.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// knownDefectTagPrefix is the tag family a scenario carries when it is red
// because of a catalogued misalignment-report §6 defect.
const knownDefectTagPrefix = "@known-defect:"

// announceKnownDefects prints one loud line per @known-defect scenario found
// under root, and returns the exact tags the default run must exclude. running
// reports whether the defect scenarios are actually being executed
// (SPEC_KNOWN_DEFECTS=1).
func announceKnownDefects(root string, running bool) []string {
	notes, tags, err := knownDefectNotes(root, running)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec: read known-defect notes: %v\n", err)
		return nil
	}
	for _, note := range notes {
		fmt.Println(note)
	}
	return tags
}

// knownDefectNotes renders the note for every known-defect scenario in the
// feature tree, in file order, and collects the tag names to exclude.
//
// The tags are returned individually because godog's filter is Behat-style with
// exact tag names — no wildcards — so the family `@known-defect:<item>` has to
// be spelled out one item at a time. Reading them from the feature tree keeps
// that list from drifting.
func knownDefectNotes(root string, running bool) ([]string, []string, error) {
	var (
		notes []string
		tags  []string
	)
	seen := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".feature") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		ids := &messages.Incrementing{}
		doc, err := gherkin.ParseGherkinDocument(file, ids.NewId)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		for _, pickle := range gherkin.Pickles(*doc, path, ids.NewId) {
			for _, tag := range pickle.Tags {
				if !strings.HasPrefix(tag.Name, knownDefectTagPrefix) {
					continue
				}
				item := strings.TrimPrefix(tag.Name, knownDefectTagPrefix)
				state := "skipped"
				if running {
					state = "running under SPEC_KNOWN_DEFECTS=1"
				}
				notes = append(notes, fmt.Sprintf(
					"KNOWN-DEFECT %s (misalignment-report §6.%s): %q — %s", item, item, pickle.Name, state))
				if !seen[tag.Name] {
					seen[tag.Name] = true
					tags = append(tags, tag.Name)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Strings(tags)
	return notes, tags, nil
}

// excludeTags turns the known-defect tag names into godog's Behat filter: one
// negated tag per catalogued item, ANDed.
func excludeTags(tags []string) string {
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		parts = append(parts, "~"+tag)
	}
	return strings.Join(parts, "&&")
}

// TestKnownDefectNotes pins the note format (gherkin-style.md §9) and the filter
// the default run applies. The real feature tree carries no @known-defect
// scenario today (the pilot's finding was fixed and its tag removed), so the
// announcer is pinned against a synthetic tree: a new @known-defect scenario
// needs nothing but its tag to be announced and excluded.
func TestKnownDefectNotes(t *testing.T) {
	root := t.TempDir()
	feature := filepath.Join(root, "pilot.feature")
	const body = `Feature: a synthetic pilot

  @known-defect:2
  Scenario: The refused stake is not yet named for the caller
    Given a step
    Then another step
`
	if err := os.WriteFile(feature, []byte(body), 0o600); err != nil {
		t.Fatalf("write synthetic feature: %v", err)
	}

	notes, tags, err := knownDefectNotes(root, false)
	if err != nil {
		t.Fatalf("read known-defect notes: %v", err)
	}
	if len(notes) == 0 {
		t.Fatalf("no known-defect scenarios announced; the synthetic pilot tags one")
	}

	joined := strings.Join(notes, "\n")
	for _, want := range []string{
		"KNOWN-DEFECT 2 (misalignment-report §6.2)",
		"\"The refused stake is not yet named for the caller\"",
		"— skipped",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not mention %q:\n%s", want, joined)
		}
	}

	if got, want := excludeTags(tags), "~@known-defect:2"; got != want {
		t.Errorf("exclusion filter = %q, want %q (godog's filter matches tag names exactly)", got, want)
	}

	runningNotes, _, err := knownDefectNotes(root, true)
	if err != nil {
		t.Fatalf("read known-defect notes: %v", err)
	}
	if !strings.Contains(strings.Join(runningNotes, "\n"), "running under SPEC_KNOWN_DEFECTS=1") {
		t.Errorf("the opt-in run's note does not say it is running:\n%s", strings.Join(runningNotes, "\n"))
	}

	// The real tree is clean of the tag: the pilot was fixed, so the default run
	// excludes nothing and announces nothing.
	realNotes, realTags, err := knownDefectNotes(featurePaths, false)
	if err != nil {
		t.Fatalf("read known-defect notes from the feature tree: %v", err)
	}
	if len(realNotes) != 0 || len(realTags) != 0 {
		t.Errorf("the feature tree still tags known defects: %v %v", realNotes, realTags)
	}
}
