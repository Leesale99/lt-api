package e2e

// Integration-test harness: every handler test in this package runs against
// a real PostgreSQL database (see internal/testdb for the setup contract).
//
//   - no LT_API_TEST_DSN and no AUDIT_SKIP_DB=1 → the suite FAILS (exit 1) with
//     instructions: a green run that tested nothing is worse than a red one.
//   - no LT_API_TEST_DSN and AUDIT_SKIP_DB=1 → a loud skip banner, then every
//     test here skips, one by one, via requireDB.
//   - LT_API_TEST_DSN set but the database is unreachable → hard failure.
//     A DSN typo must never read as a skip.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/testdb"
)

var testPool *pgxpool.Pool

// Per-context pools: the E2E suite is the composition root (compose_test.go
// mirrors cmd/api/main.go) and wires its app with these — the handlers run
// under the real privilege regime. testPool (owner) plants fixtures.
var (
	schedulePool *pgxpool.Pool
	gamePool     *pgxpool.Pool
	identityPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	// Registration flows hash through the real handler; the assertions read
	// the stored hash back (bcrypt.CompareHashAndPassword), so the cheapest
	// cost proves the same thing.
	identity.SetPasswordHashCost(bcrypt.MinCost)

	dsn := os.Getenv(testdb.DSNEnv)
	switch decision := testdb.DecideSkip(dsn, os.Getenv(testdb.SkipOptOutEnv)); decision.Action {
	case testdb.ActionFail:
		// Not a skip: every test here is DB-backed, so the exit status must say
		// the suite tested nothing. The message carries the way out.
		fmt.Fprintln(os.Stderr, decision.Message)
		os.Exit(1)
	case testdb.ActionSkip:
		testdb.Announce(decision.Message)
		os.Exit(m.Run())
	}

	db, teardown, err := testdb.Setup(
		context.Background(),
		dsn,
		"server",
		"../../migrations/000001_create_initial_game_models.up.sql",
		"../../migrations/000002_create_users_table.up.sql",
		"../../migrations/000003_create_user_tokens_table.up.sql",
		"../../migrations/000004_add_permissions.up.sql",
		"../../migrations/000005_add_matches_ended_at.up.sql",
		// Ride persistence: phase 03 replaced the in-memory ride stub with
		// the rides table — the ride handler tests run against it.
		"../../migrations/000006_create_rides_table.up.sql",
		"../../migrations/000007_alter_matches_odds_precision.up.sql",
		// Round lifecycle gates (000008): 000015 moves their functions into
		// the schedule schema, so the chain must be complete.
		"../../migrations/000008_add_round_lifecycle_gates.up.sql",
		"../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
		// Idempotency keys (000010): the lock handler claims and replays
		// through the real table.
		"../../migrations/000010_add_idempotency_keys_table.up.sql",
		// Result agreement (000011) + INSERT state gate (000012) + money-
		// state coupling (000013): ride handler tests exercise the full
		// gate surface.
		"../../migrations/000011_add_rides_result_agreement_gate.up.sql",
		"../../migrations/000012_add_rides_insert_state_gate.up.sql",
		"../../migrations/000013_add_rides_money_state_coupling.up.sql",
		// Relaxed close gate (000014): the event seam moved the ride
		// settlement behind RoundCompleted, so undecided rides no longer
		// refuse a round close.
		"../../migrations/000014_relax_rounds_close_gate.up.sql",
		// 000015–000018: the context schemas, the cross-context
		// FK/trigger surgery and the per-context roles — the E2E suite runs
		// its stores on the role-restricted pools these create.
		"../../migrations/000015_split_context_schemas.up.sql",
		"../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
		"../../migrations/000017_app_level_result_agreement.up.sql",
		"../../migrations/000018_context_roles.up.sql",
		// 000019: the status-rank gates — the DB now refuses match/round/
		// season regressions, so the advisory 422 is not the only guard. The
		// API path reaches the 422 first; the direct fixtures here stay legal.
		"../../migrations/000019_add_status_rank_gates.up.sql",
		// 000020: the ended_at stamp keeps a supplied end time (the resolve
		// flow's actual end) and only stamps now() when none is supplied —
		// the E2E resolve test reads the row back against the echoed value.
		"../../migrations/000020_respect_supplied_match_ended_at.up.sql",
		// 000021: the game-side definer delete behind the season delete
		// policy — the E2E season tests exercise it over the role pools
		// 000018 creates.
		"../../migrations/000021_add_game_delete_players_for_season.up.sql",
		"../../migrations/000022_scope_idempotency_keys_per_caller.up.sql",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	testPool = db.Owner
	schedulePool = db.Schedule
	gamePool = db.Game
	identityPool = db.Identity

	code := m.Run()
	teardown()
	os.Exit(code)
}

// requireDB skips a test under the explicit AUDIT_SKIP_DB=1 opt-out (TestMain
// leaves testPool nil there); otherwise TestMain has already failed or built it.
func requireDB(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("database skipped: AUDIT_SKIP_DB=1 opted out")
	}
}
