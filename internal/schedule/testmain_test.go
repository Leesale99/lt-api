package schedule

// Integration-test harness for the schedule package: DB-backed store and
// gate tests run against a real PostgreSQL database (see internal/testdb
// for the setup contract). The pure-logic validator tests (lifecycle,
// matches_validation, round_phase) run without it.
//
//   - LT_API_TEST_DSN unset → DB-backed tests skip, so `go test ./...`
//     still passes on a DB-less machine.
//   - LT_API_TEST_DSN set but the database is unreachable → hard failure.
//     A green run that silently tested nothing is worse than a red one.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/testdb"
)

// migFile lists all migrations the schedule tests need. The base calendar
// hierarchy is 000001; 000005 adds matches.ended_at (the close/resolve
// paths read it). The rides migrations (000006/11/12/13) are here because
// RoundStore.Close still auto-resolves undecided rides — the temporary
// cross-context write of the Phase B split (see rounds.go) — and the ride
// planting helpers walk through the same UPDATE gates every writer faces.
// They leave at the RoundCompleted event seam in Phase C, together with
// that write.
var migFiles = []string{
	"../../migrations/000001_create_initial_game_models.up.sql",
	"../../migrations/000005_add_matches_ended_at.up.sql",
	"../../migrations/000006_create_rides_table.up.sql",
	"../../migrations/000008_add_round_lifecycle_gates.up.sql",
	"../../migrations/000011_add_rides_result_agreement_gate.up.sql",
	"../../migrations/000012_add_rides_insert_state_gate.up.sql",
	"../../migrations/000013_add_rides_money_state_coupling.up.sql",
}

// PostgreSQL error codes (see pgerrcode; inlined to avoid the extra dependency).
const (
	errCheckViolation    = "23514"
	errForeignKey        = "23503"
	errRestrictViolation = "23001"
	errUniqueViolation   = "23505"
)

var pool *pgxpool.Pool

// TestMain always runs the full suite: unit tests in this package must run
// on a DB-less machine too. Only the DB-backed tests skip, one by one, via
// requireDB. Exiting before m.Run() would silently disable the whole
// package — including `go test -list`.
func TestMain(m *testing.M) {
	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		fmt.Println("LT_API_TEST_DSN not set; database-backed tests will be skipped")
		os.Exit(m.Run())
	}

	ctx := context.Background()

	// Fresh database per run: the migration itself is under test.
	var teardown func()
	var err error
	pool, teardown, err = testdb.Setup(ctx, dsn, "schedule", migFiles...)
	if err != nil {
		// DSN set but unusable: fail loudly rather than report a green run
		// that tested nothing.
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}

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
			INSERT INTO teams (name, logo, description) VALUES ('Olympiacos', 'https://x.example/logo.png', 'desc') RETURNING id
		), t2 AS (
			INSERT INTO teams (name, logo, description) VALUES ('Real Madrid', 'https://x.example/logo.png', 'desc') RETURNING id
		), s AS (
			INSERT INTO seasons (status) VALUES ('in_progress') RETURNING id
		), r AS (
			INSERT INTO rounds (season_id, number, status) SELECT s.id, 1, 'open' FROM s RETURNING id, season_id
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
	if _, err := pool.Exec(ctx, `TRUNCATE matches, rounds, seasons, teams RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

// wantCode: "" means the statement must succeed; otherwise the pg error code expected.
type constraintCase struct {
	name     string
	query    string
	args     []any
	wantCode string
}

func assertCode(t *testing.T, c constraintCase, err error) {
	t.Helper()

	if c.wantCode == "" {
		if err != nil {
			t.Errorf("%s: expected success, got: %v", c.name, err)
		}
		return
	}
	if err == nil {
		t.Errorf("%s: expected error code %s, got success", c.name, c.wantCode)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: expected pg error, got: %T %v", c.name, err, err)
		return
	}
	if pgErr.Code != c.wantCode {
		t.Errorf("%s: got error code %s (%s), want %s", c.name, pgErr.Code, pgErr.Message, c.wantCode)
	}
}
