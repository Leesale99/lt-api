package main

// Tests for `spec work` (work.go): the work queue and its priority order, the
// module filter, the per-rule overlay (field table, the resolution tables, the
// linked scenarios, the gate), and the error paths. The fixtures are the same
// synthetic checkouts the checker's tests use — a testdata feature tree plus a
// catalog built in the test, never the real one.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// workCatalog is the overlay fixture: a happy interface rule carrying every
// field a work order renders (fixture, pinned_by, refusal, expert and knowledge
// ref, a unit pointer and two scenarios tagged at the group), a database rule
// whose authority deviates from its category default (with the reason in the
// rule file's Notes body), and a specified-only rule.
func workCatalog() Catalog {
	born := baseRule() // R-KERNEL-001
	born.Module = "game"
	born.Entity = "ride"
	born.Title = "Rides are born locked"
	born.Statement = "A ride is born locked; the client cannot choose another state."
	born.Category = "interface"
	born.Authority = "handler"
	born.Refusal = &Refusal{Code: "synthetic", Sentinel: ptr("ErrInvalidTransition"), HTTP: ptr(422)}
	born.Fixture = ptr("rideBirthRule")
	born.Enforcement[0].PinnedBy = ptr("rideBirthRule")
	born.Verification = Verification{
		Unit: []string{"internal/kernel/thing/thing_test.go::TestDo"},
		Gherkin: []string{
			"docs/spec/features/happy/ride.feature::A new ride is born locked",
			"docs/spec/features/happy/ride.feature::A ride cannot be born in any other state",
		},
	}
	born.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
	born.KnowledgeRef = ptr("docs/league-tokens-learning-phases/phase-03.md")

	money := baseRule()
	money.ID = "R-KERNEL-002"
	money.Module = "game"
	money.Entity = "ride"
	money.Title = "Money and state move together"
	money.Statement = "A paid ride cannot lose its money without leaving its state."
	money.Category = "policy"    // default authority: domain
	money.Authority = "database" // the deviation the overlay must explain
	money.Mechanism = ptr("constraint")
	money.WriteScope = ptr("row-local")
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
	money.Verification = Verification{Unit: []string{"internal/kernel/thing/thing_test.go::TestDo"}}
	money.Notes = "The predicate is row-local, so the database decides it."

	config := baseRule()
	config.ID = "R-PLATFORM-001"
	config.Module = "platform"
	config.Entity = "config"
	config.Title = "Configuration is parsed once"
	config.Statement = "Configuration is read once at start-up."
	config.Status = statusSpecifiedOnly
	config.Enforcement = nil
	config.Verification = Verification{}
	config.Fixture = nil
	config.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")
	config.Notes = "Specified only."

	return Catalog{config, born, money}
}

// queueCatalog is the queue fixture, written in an order that is NOT the
// emission order: an implemented rule with a stale site first, then the
// specified-only rule, then the drifted one. A queue that leaked catalog order
// would fail every ordering assertion below.
func queueCatalog() Catalog {
	broken := baseRule() // R-KERNEL-001, resolves in the synthetic repo
	broken.ID = "R-PLATFORM-001"
	broken.Module = "platform"
	broken.Entity = "config"
	broken.Title = "Configuration is parsed once"
	broken.Enforcement = []Enforcement{{
		Site:  "internal/kernel/thing/gone.go::Do",
		Layer: "domain",
		Role:  "authoritative",
	}}

	specified := baseRule()
	specified.ID = "R-LEDGER-001"
	specified.Module = "ledger"
	specified.Entity = "entry"
	specified.Title = "Entries balance"
	specified.Status = statusSpecifiedOnly
	specified.Enforcement = nil
	specified.Verification = Verification{}
	specified.ExpertRef = ptr("docs/league-tokens-learning-phases/phase-03.md")

	drifted := baseRule()
	drifted.ID = "R-GAME-001"
	drifted.Module = "game"
	drifted.Entity = "ride"
	drifted.Title = "Rides are born locked"
	drifted.Status = statusDrifted
	drifted.Notes = "The handler still accepts a client-chosen state."

	return Catalog{broken, specified, drifted}
}

func runWorkTest(t *testing.T, repo *testRepo, args ...string) (int, string, string) {
	t.Helper()
	args = append([]string{"--rules", repo.rulesPath}, args...)
	var stdout, stderr bytes.Buffer
	code := runWork(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestWorkRuleOverlay pins the one-rule work order: the derived content only.
// The statement and the notes body are NOT reprinted — they live in the rule
// file, and the overlay links to it.
func TestWorkRuleOverlay(t *testing.T) {
	repo := gherkinRepo(t, "features/happy/ride.feature")
	repo.writeCatalog(t, workCatalog())

	code, out, errOut := runWorkTest(t, repo, "R-KERNEL-001")
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}

	ruleFile := displayPath(filepath.Join(repo.rulesPath, "game", "R-KERNEL-001.md"))
	for _, want := range []string{
		"GENERATED by spec work",
		"# Work order — R-KERNEL-001: Rides are born locked",
		"> Generated by `go run ./cmd/spec work R-KERNEL-001` from the canonical catalog",
		"`" + ruleFile + "`",
		"| module | `game` |",
		"| entity | `ride` |",
		"| category | `interface` |",
		"| status | `implemented` |",
		"| authority | `handler` |",
		"| `internal/kernel/thing/thing.go::Do` | `domain` | `authoritative` | `rideBirthRule` | yes |",
		"| `synthetic` | _(unnamed)_ | `ErrInvalidTransition` | `422` |",
		"**Shared fixture.** `rideBirthRule`",
		"| `unit` | `internal/kernel/thing/thing_test.go::TestDo` | yes |",
		"| `gherkin` | `docs/spec/features/happy/ride.feature::A new ride is born locked` | yes |",
		"**Linked scenarios** (tagged `@rule:R-KERNEL-001`)",
		"- `docs/spec/features/happy/ride.feature::A new ride is born locked`",
		"- `docs/spec/features/happy/ride.feature::A ride cannot be born in any other state`",
		"**Expert ref** `docs/league-tokens-learning-phases/phase-03.md`",
		"**Knowledge ref** `docs/league-tokens-learning-phases/phase-03.md`",
		"**Gate**",
		"make audit",
		"make spec/check",
		"make spec/gen",
		"make test/spec",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("work order is missing %q", want)
		}
	}

	// The authored prose is the rule file's, not the overlay's.
	for _, unwanted := range []string{
		"**Statement.**",
		"client cannot choose another state",
		"**Notes**",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("work order duplicated authored prose: %q\n%s", unwanted, out)
		}
	}
	// The scenario tagged with the sibling rule is not this rule's scenario.
	if strings.Contains(out, "A lost match ends the ride") {
		t.Error("work order linked a scenario tagged with a different rule")
	}
	// No deviation: category `interface` defaults to `handler`.
	if strings.Contains(out, "Authority deviation") {
		t.Error("work order reported a deviation where authority matches the category default")
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Error("work order should end with exactly one newline")
	}
}

// TestWorkRuleAuthorityDeviation pins the fields the module-block test used to
// cover: the deviation is promoted to a pointer into the rule file's Notes, the
// mechanism/write_scope rows are rendered (write_scope under its YAML name), and
// the note prose itself is not copied into the overlay.
func TestWorkRuleAuthorityDeviation(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, workCatalog())

	code, out, errOut := runWorkTest(t, repo, "R-KERNEL-002")
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"| mechanism | `constraint` |",
		"| write_scope | `row-local` |",
		"**Authority deviation.** category `policy` defaults to `domain`; this rule's authority is `database`. " +
			"The reason is in the rule file's `## Notes` body.",
		"| `23514` | `rides_money_state_coupling` | `ErrInvalidTransition` | `409` |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("work order is missing %q", want)
		}
	}
	if strings.Contains(out, "| scope |") {
		t.Error("work order still labels write_scope with the retired `scope` name")
	}
	if strings.Contains(out, "The predicate is row-local") {
		t.Error("work order copied the rule file's notes instead of pointing at them")
	}
}

func TestWorkRuleSpecifiedOnly(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, workCatalog())

	code, out, errOut := runWorkTest(t, repo, "R-PLATFORM-001")
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"| status | `specified-only` |",
		"**Enforcement.** _(no site — a specified-only rule has none yet)_",
		"**Refusal.** _(none — this rule cannot refuse)_",
		"**Verification.** _(no pointer — policy 3 flags an implemented rule without one)_",
		"**Linked scenarios.** _(none tagged `@rule:R-PLATFORM-001` yet)_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("specified-only work order is missing %q", want)
		}
	}
	if strings.Contains(out, "Specified only.") {
		t.Error("work order copied the rule file's notes instead of pointing at them")
	}
}

func TestWorkMarksUnresolvedPointers(t *testing.T) {
	repo := newTestRepo(t)
	catalog := workCatalog()
	// A stale Go pointer and a scenario no feature file carries.
	catalog[1].Verification.Unit = []string{"internal/kernel/thing/gone_test.go::TestGone"}
	catalog[1].Verification.Gherkin = []string{"docs/spec/features/happy/ride.feature::A scenario that moved"}
	catalog[1].Enforcement[0].Site = "internal/kernel/thing/gone.go::Do"
	repo.writeCatalog(t, catalog)

	code, out, errOut := runWorkTest(t, repo, "R-KERNEL-001")
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"| `unit` | `internal/kernel/thing/gone_test.go::TestGone` | **missing** |",
		"| `gherkin` | `docs/spec/features/happy/ride.feature::A scenario that moved` | **missing** |",
		"**Linked scenarios.** _(none tagged `@rule:R-KERNEL-001` yet)_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("work order is missing %q", want)
		}
	}
	// Resolution is a display fact, never a failure of the work order itself.
	if strings.Contains(out, "`missing`") {
		t.Error("work order rendered the resolution column as code")
	}
}

func TestWorkUnknownRuleID(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, workCatalog())

	code, out, errOut := runWorkTest(t, repo, "R-NOPE-999")
	if code == 0 {
		t.Fatal("runWork of an unknown rule id exited 0")
	}
	if out != "" {
		t.Errorf("unknown rule id wrote %q to stdout", out)
	}
	if !strings.Contains(errOut, `unknown rule id "R-NOPE-999"`) {
		t.Errorf("stderr = %q, want an unknown-rule-id message", errOut)
	}
}

// TestWorkEnforcementResolvesColumn pins writeWorkEnforcement's Resolves
// column: every site is resolved the way `check` resolves it today, and a site
// that is malformed or absent is **missing**.
func TestWorkEnforcementResolvesColumn(t *testing.T) {
	repo := newTestRepo(t)
	repo.addFile(t, "migrations/000001_create_initial_game_models.up.sql", "-- schema\n")
	repo.addFile(t, "internal/kernel/thing/gadget.go",
		"package thing\n\ntype Widget struct{}\n\ntype Gadget struct{}\n\nfunc (g *Gadget) Run() {}\n")

	catalog := workCatalog()
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

	code, out, errOut := runWorkTest(t, repo, "R-KERNEL-001")
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
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
			t.Errorf("work order is missing %q", want)
		}
	}
}

// TestWorkStructuredNotesEscapeMarkdownStructure pins the escaping the shared
// note renderer applies to the overlay's stale-pointer/tie blocks: a nested
// quote, a heading, and a list marker must not change the quote's structure.
func TestWorkStructuredNotesEscapeMarkdownStructure(t *testing.T) {
	rule := baseRule()
	rule.StalePointer = ptr(strings.Join([]string{
		"Plain prose stays exactly as written.",
		"> a nested quote",
		"# a heading",
		"- a list item",
		"1. an ordered item",
	}, "\n"))
	rule.Tie = ptr("Tied to the authority; `-not-a-list-marker` is not escaped.")

	var b strings.Builder
	writeStructuredNotes(&b, rule)
	out := b.String()

	for _, want := range []string{
		"**Stale pointer**",
		"> Plain prose stays exactly as written.",
		"> \\> a nested quote",
		"> \\# a heading",
		"> \\- a list item",
		"> 1\\. an ordered item",
		"**Tie**",
		"> Tied to the authority; `-not-a-list-marker` is not escaped.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("note block is missing %q; got:\n%s", want, out)
		}
	}
}

func TestWorkQueuePriorityOrder(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, queueCatalog())

	code, out, errOut := runWorkTest(t, repo)
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}

	drifted := strings.Index(out, "| 1 | `R-GAME-001` |")
	specified := strings.Index(out, "| 2 | `R-LEDGER-001` |")
	broken := strings.Index(out, "| 3 | `R-PLATFORM-001` |")
	if drifted < 0 || specified < 0 || broken < 0 {
		t.Fatalf("queue is missing a priority row:\n%s", out)
	}
	if !(drifted < specified && specified < broken) {
		t.Errorf("queue is not ordered drifted → specified-only → broken pointer:\n%s", out)
	}

	for _, want := range []string{
		"# Spec work queue",
		"GENERATED by spec work",
		"**3 rules** — 1 drifted, 1 implemented, 1 specified-only.",
		"**Priority order.** 1 drifted",
		"| Priority | Rule | Title | Module | Status | Reason |",
		"| 1 | `R-GAME-001` | Rides are born locked | `game` | `drifted` | " +
			"catalog status is drifted; notes: The handler still accepts a client-chosen state. |",
		"| 2 | `R-LEDGER-001` | Entries balance | `ledger` | `specified-only` | " +
			"designed but not implemented; expert-ref docs/league-tokens-learning-phases/phase-03.md |",
		"| 3 | `R-PLATFORM-001` | Configuration is parsed once | `platform` | `implemented` | " +
			"unresolved pointer: enforcement site internal/kernel/thing/gone.go::Do |",
		"**Next step.** Take the top row; `make spec/work args='R-GAME-001'`",
		"(or `go run ./cmd/spec work R-GAME-001`)",
		"**Not evaluated here.**",
		"make spec/check",
		"make audit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("queue is missing %q", want)
		}
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Error("queue should end with exactly one newline")
	}
}

func TestWorkQueueBrokenVerificationPointer(t *testing.T) {
	repo := newTestRepo(t)
	catalog := baseCatalog()
	catalog[0].Verification = Verification{
		Unit: []string{"internal/kernel/thing/thing_test.go::TestGone"},
		E2E:  []string{"internal/kernel/thing/missing_test.go::TestE2E"},
	}
	repo.writeCatalog(t, catalog)

	code, out, errOut := runWorkTest(t, repo)
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"| 3 | `R-KERNEL-001` |",
		"unit pointer internal/kernel/thing/thing_test.go::TestGone",
		"e2e pointer internal/kernel/thing/missing_test.go::TestE2E",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("queue is missing %q:\n%s", want, out)
		}
	}
}

func TestWorkQueueResolvingRuleIsNotQueued(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, baseCatalog())

	code, out, errOut := runWorkTest(t, repo)
	if code != 0 {
		t.Fatalf("runWork = %d, stderr=%s", code, errOut)
	}
	if strings.Contains(out, "R-KERNEL-001 |") {
		t.Errorf("a fully resolving implemented rule was queued:\n%s", out)
	}
	if !strings.Contains(out, "**The queue is empty**") {
		t.Errorf("queue did not say it was empty:\n%s", out)
	}
}

func TestWorkQueueEmptyCatalog(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, Catalog{})

	code, out, errOut := runWorkTest(t, repo)
	if code != 0 {
		t.Fatalf("runWork of an empty catalog = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"**No rule in scope**",
		"**The queue is empty**",
		"make spec/check",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("empty queue is missing %q:\n%s", want, out)
		}
	}
}

func TestWorkQueueModuleFilter(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, queueCatalog())

	code, out, errOut := runWorkTest(t, repo, "--module", "ledger")
	if code != 0 {
		t.Fatalf("runWork --module ledger = %d, stderr=%s", code, errOut)
	}
	for _, want := range []string{
		"module `ledger`",
		"**1 rule** — 1 specified-only.",
		"| 2 | `R-LEDGER-001` |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("module queue is missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"R-GAME-001", "R-PLATFORM-001"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("module queue leaked %q:\n%s", unwanted, out)
		}
	}
}

func TestWorkUnknownModule(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, workCatalog())

	code, out, errOut := runWorkTest(t, repo, "--module", "nope")
	if code != 2 {
		t.Fatalf("runWork --module nope = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("unknown module wrote %q to stdout", out)
	}
	if !strings.Contains(errOut, `unknown module "nope"`) {
		t.Errorf("stderr = %q, want an unknown-module message", errOut)
	}
	for _, module := range []string{"game", "identity", "kernel", "ledger", "platform", "schedule"} {
		if !strings.Contains(errOut, module) {
			t.Errorf("unknown-module message does not name %q", module)
		}
	}
}

func TestWorkUsageErrors(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, workCatalog())

	for _, args := range [][]string{
		{"--nope"},
		{"R-KERNEL-001", "R-KERNEL-002"},
		{"--module", "game", "R-KERNEL-001"},
	} {
		code, out, errOut := runWorkTest(t, repo, args...)
		if code != 2 {
			t.Errorf("runWork(%v) = %d, want 2", args, code)
		}
		if out != "" {
			t.Errorf("runWork(%v) wrote %q to stdout", args, out)
		}
		if !strings.Contains(errOut, "spec work:") {
			t.Errorf("runWork(%v) stderr = %q, want a spec work error", args, errOut)
		}
	}
}

func TestWorkLoadError(t *testing.T) {
	repo := newTestRepo(t)

	var stdout, stderr bytes.Buffer
	code := runWork([]string{"--rules", filepath.Join(repo.root, "docs", "spec", "missing")}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("runWork of a missing catalog = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("load error wrote %q to stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), "spec work:") {
		t.Errorf("load error stderr = %q, want a spec work message", stderr.String())
	}
}

func TestWorkDeterministic(t *testing.T) {
	repo := newTestRepo(t)
	repo.writeCatalog(t, queueCatalog())

	codeFirst, first, errFirst := runWorkTest(t, repo)
	codeSecond, second, errSecond := runWorkTest(t, repo)
	if codeFirst != 0 || codeSecond != 0 {
		t.Fatalf("runWork = %d/%d, stderr=%s%s", codeFirst, codeSecond, errFirst, errSecond)
	}
	if first != second {
		t.Errorf("two runs differ:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
