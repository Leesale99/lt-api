package service

// Integration-test harness for the schedule service's use cases: the close
// tests (rounds_store_test.go) drive real transactions against a real
// PostgreSQL database (see internal/testdb for the setup contract).
//
//   - LT_API_TEST_DSN unset → DB-backed tests skip, so `go test ./...`
//     still passes on a DB-less machine.
//   - LT_API_TEST_DSN set but the database is unreachable → hard failure.
//     A green run that silently tested nothing is worse than a red one.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/testdb"
)

// migFiles lists all migrations the service tests need — the FULL series
// since 000015: that migration moves whatever tables exist (it cannot run
// against a partial chain), 000016–000018 (FK surgery, roles) assume it,
// and the suite runs its repositories on the role-restricted pool 000018
// creates. The calendar hierarchy is 000001; 000005 adds
// matches.ended_at (the close/resolve paths read it). The rides
// migrations (000006/11/12/13) are here because the ride fixtures plant
// rides and walk through the same UPDATE gates every writer faces.
// 000014 relaxes the unresolved-rides half of rounds_close_gate — since
// the event seam the ride settlement is the game context's,
// behind RoundCompleted.
var migFiles = []string{
	"../../../../migrations/000001_create_initial_game_models.up.sql",
	"../../../../migrations/000002_create_users_table.up.sql",
	"../../../../migrations/000003_create_user_tokens_table.up.sql",
	"../../../../migrations/000004_add_permissions.up.sql",
	"../../../../migrations/000005_add_matches_ended_at.up.sql",
	"../../../../migrations/000006_create_rides_table.up.sql",
	"../../../../migrations/000007_alter_matches_odds_precision.up.sql",
	"../../../../migrations/000008_add_round_lifecycle_gates.up.sql",
	"../../../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
	"../../../../migrations/000010_add_idempotency_keys_table.up.sql",
	"../../../../migrations/000011_add_rides_result_agreement_gate.up.sql",
	"../../../../migrations/000012_add_rides_insert_state_gate.up.sql",
	"../../../../migrations/000013_add_rides_money_state_coupling.up.sql",
	"../../../../migrations/000014_relax_rounds_close_gate.up.sql",
	"../../../../migrations/000015_split_context_schemas.up.sql",
	"../../../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
	"../../../../migrations/000017_app_level_result_agreement.up.sql",
	"../../../../migrations/000018_context_roles.up.sql",
	// 000019-000021 run the full production chain: the status-rank gates, the
	// ended_at stamp that keeps a supplied end time, and the game-side
	// definer delete.
	"../../../../migrations/000019_add_status_rank_gates.up.sql",
	"../../../../migrations/000020_respect_supplied_match_ended_at.up.sql",
	"../../../../migrations/000021_add_game_delete_players_for_season.up.sql",
}

var pool *pgxpool.Pool

// schedulePool runs as schedule_app: the repositories under test exercise
// the real privilege regime; pool (owner) plants fixtures — including
// cross-schema ones (rides/players are the game context's, and the ride
// fixtures keep planting them as the composition owner).
var schedulePool *pgxpool.Pool

// TestMain always runs the full suite: unit tests in this package must run
// on a DB-less machine too. Only the DB-backed tests skip, one by one, via
// requireDB.
func TestMain(m *testing.M) {
	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		fmt.Println("LT_API_TEST_DSN not set; database-backed tests will be skipped")
		os.Exit(m.Run())
	}

	ctx := context.Background()

	// Fresh database per run: the migration itself is under test.
	db, teardown, err := testdb.Setup(ctx, dsn, "schedule_service", migFiles...)
	if err != nil {
		// DSN set but unusable: fail loudly rather than report a green run
		// that tested nothing.
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	pool = db.Owner
	schedulePool = db.Schedule

	code := m.Run()
	teardown()
	os.Exit(code)
}

// requireDB skips a test when the database harness is unavailable.
func requireDB(t *testing.T) {
	t.Helper()
	if pool == nil {
		t.Skip("LT_API_TEST_DSN not set; requires a test database")
	}
}

// seed inserts the base fixture (2 teams, 1 season, 1 round) and returns their IDs.
func seed(ctx context.Context, t *testing.T) (seasonID, roundID, homeID, awayID int) {
	t.Helper()

	err := pool.QueryRow(ctx, `
		WITH t1 AS (
			INSERT INTO schedule.teams (name, logo, description) VALUES ('Olympiacos', 'https://x.example/logo.png', 'desc') RETURNING id
		), t2 AS (
			INSERT INTO schedule.teams (name, logo, description) VALUES ('Real Madrid', 'https://x.example/logo.png', 'desc') RETURNING id
		), s AS (
			INSERT INTO schedule.seasons (status) VALUES ('in_progress') RETURNING id
		), r AS (
			INSERT INTO schedule.rounds (season_id, number, status) SELECT s.id, 1, 'open' FROM s RETURNING id, season_id
		)
		SELECT s.id, r.id, t1.id, t2.id FROM s, r, t1, t2
	`).Scan(&seasonID, &roundID, &homeID, &awayID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return seasonID, roundID, homeID, awayID
}

func cleanup(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE schedule.matches, game.rides, game.players, schedule.rounds, schedule.seasons, schedule.teams RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}
