package spec

// TestMain is the suite's harness: one throwaway database built from the
// repository's migrations, the modules composed over it (see doc.go), then the
// godog suite, then the plain Go tests.
//
// The DSN contract is testdb.DecideSkip's (internal/testdb/skip.go), and it is
// deliberately the opposite of a quiet skip:
//
//   - LT_API_TEST_DSN unset and AUDIT_SKIP_DB unset → the suite FAILS (exit 1)
//     with instructions for bringing the compose database up and exporting the
//     DSN. A green run that tested nothing is worse than a red one.
//   - LT_API_TEST_DSN unset and AUDIT_SKIP_DB=1 → a loud SKIP banner, then the
//     DB-free tests in this package still run (exit 0 if they pass). Opting out
//     has to be said out loud.
//   - LT_API_TEST_DSN set but the database unreachable → hard failure with
//     testdb's actionable message.
//
// internal/e2e still skips quietly without the DSN; moving it onto the same
// policy is a separate change.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/cucumber/godog"
	"golang.org/x/crypto/bcrypt"

	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/testdb"
)

// godogOpts is the suite's run configuration. runMain installs the tag filter
// that keeps every catalogued known-defect scenario out of the default run
// (style guide §9) and SPEC_KNOWN_DEFECTS=1 clears it so those scenarios
// execute and report their outcome. The filter is built from the feature tree
// rather than written as "~@known-defect" because godog's Behat filter matches
// tag names exactly — no wildcards — and the catalogued tags are
// @known-defect:<item>.
var godogOpts = godog.Options{
	Output:   os.Stdout,
	Format:   "progress",
	Paths:    []string{"../../docs/spec/features"},
	NoColors: true,
}

// featurePaths is the directory the known-defect notes are read from (the same
// tree godog parses).
const featurePaths = "../../docs/spec/features"

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// runMain owns the single os.Exit: harness setup, the known-defect notes, the
// godog suite, then m.Run(). The exit status is the worse of the two.
func runMain(m *testing.M) int {
	// The registration and password scenarios hash for real (dozens of them);
	// the catalog pins the behaviour, never how long a hash takes.
	identity.SetPasswordHashCost(bcrypt.MinCost)

	dsn := os.Getenv(testdb.DSNEnv)
	switch decision := testdb.DecideSkip(dsn, os.Getenv(testdb.SkipOptOutEnv)); decision.Action {
	case testdb.ActionFail:
		// Not a skip: the enforcement half of the audit did not run, so the
		// exit status says so. The message carries the way out.
		fmt.Fprintln(os.Stderr, decision.Message)
		return 1
	case testdb.ActionSkip:
		// Announced, not merely printed: `go test ./...` discards a green
		// package's output, and an unseen skip is the silent skip this
		// policy exists to prevent.
		testdb.Announce(decision.Message)
		return m.Run()
	}

	db, teardown, err := testdb.Setup(
		context.Background(),
		dsn,
		"spec",
		"../../migrations/000001_create_initial_game_models.up.sql",
		"../../migrations/000002_create_users_table.up.sql",
		"../../migrations/000003_create_user_tokens_table.up.sql",
		"../../migrations/000004_add_permissions.up.sql",
		"../../migrations/000005_add_matches_ended_at.up.sql",
		"../../migrations/000006_create_rides_table.up.sql",
		"../../migrations/000007_alter_matches_odds_precision.up.sql",
		"../../migrations/000008_add_round_lifecycle_gates.up.sql",
		"../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
		"../../migrations/000010_add_idempotency_keys_table.up.sql",
		"../../migrations/000011_add_rides_result_agreement_gate.up.sql",
		"../../migrations/000012_add_rides_insert_state_gate.up.sql",
		"../../migrations/000013_add_rides_money_state_coupling.up.sql",
		"../../migrations/000014_relax_rounds_close_gate.up.sql",
		"../../migrations/000015_split_context_schemas.up.sql",
		"../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
		"../../migrations/000017_app_level_result_agreement.up.sql",
		"../../migrations/000018_context_roles.up.sql",
		"../../migrations/000019_add_status_rank_gates.up.sql",
		"../../migrations/000020_respect_supplied_match_ended_at.up.sql",
		"../../migrations/000021_add_game_delete_players_for_season.up.sql",
		"../../migrations/000022_scope_idempotency_keys_per_caller.up.sql",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		return 1
	}
	defer teardown()

	if err := composeSpecApplication(db); err != nil {
		fmt.Fprintf(os.Stderr, "compose spec application: %v\n", err)
		return 1
	}

	running := os.Getenv("SPEC_KNOWN_DEFECTS") == "1"
	defectTags := announceKnownDefects(featurePaths, running)
	if running {
		godogOpts.Tags = ""
	} else {
		godogOpts.Tags = excludeTags(defectTags)
	}

	status := godog.TestSuite{
		Name:                "spec",
		ScenarioInitializer: InitializeScenario,
		Options:             &godogOpts,
	}.Run()

	if got := m.Run(); got > status {
		status = got
	}

	return status
}
