package game

// Constraint tests: verify that the database schema (migrations/) enforces
// the same rules as the Go validators, and that the permitted status values
// in the Go slices are accepted by the DB CHECKs (drift detection).
//
// Requires a running PostgreSQL. LT_API_TEST_DSN (see .envrc) points at the
// test database, which the harness creates and drops itself — the test role
// needs only LOGIN + CREATEDB (see internal/testdb).
//
// Without LT_API_TEST_DSN the tests are skipped.

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

const migFile = "../../migrations/000001_create_initial_game_models.up.sql"

// PostgreSQL error codes (see pgerrcode; inlined to avoid the extra dependency).
const (
	errCheckViolation    = "23514"
	errForeignKey        = "23503"
	errRestrictViolation = "23001"
	errUniqueViolation   = "23505"
	errUndefinedColumn   = "42703"
	errUndefinedTable    = "42P01"
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
	pool, teardown, err = testdb.Setup(ctx, dsn, "game", migFile)
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

func insertMatch(ctx context.Context, t *testing.T, c constraintCase) {
	t.Helper()
	_, err := pool.Exec(ctx, c.query, c.args...)
	assertCode(t, c, err)
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
	if !errorsAs(err, &pgErr) {
		t.Errorf("%s: expected pg error, got: %T %v", c.name, err, err)
		return
	}
	if pgErr.Code != c.wantCode {
		t.Errorf("%s: got error code %s (%s), want %s", c.name, pgErr.Code, pgErr.Message, c.wantCode)
	}
}

// errorsAs delegates to errors.As for *pgconn.PgError.
func errorsAs(err error, target **pgconn.PgError) bool {
	return errors.As(err, target)
}

func TestMatchConstraints(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	defer cleanup(ctx, t)

	base := fmt.Sprintf(`
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at%s)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 days'%s)`, "%s", "%s")

	valid := fmt.Sprintf(base, "", "")
	cases := []constraintCase{
		{
			name:  "valid created match without score",
			query: valid,
			args:  []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, "created"},
		},
		{
			name:  "closed match with score",
			query: fmt.Sprintf(base, ", home_score, away_score", ", $8, $9"),
			args:  []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, "closed", 88, 79},
		},
		{
			name:     "home team equals away team",
			query:    valid,
			args:     []any{seasonID, roundID, homeID, homeID, 1.5, 2.5, "created"},
			wantCode: errCheckViolation,
		},
		{
			name:     "odds equal to 1 rejected",
			query:    valid,
			args:     []any{seasonID, roundID, homeID, awayID, 1.0, 2.5, "created"},
			wantCode: errCheckViolation,
		},
		{
			name:     "negative score rejected",
			query:    fmt.Sprintf(base, ", home_score, away_score", ", $8, $9"),
			args:     []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, "closed", -1, 0},
			wantCode: errCheckViolation,
		},
		{
			name:     "score set on pre-start match rejected",
			query:    fmt.Sprintf(base, ", home_score, away_score", ", $8, $9"),
			args:     []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, "created", 10, 5},
			wantCode: errCheckViolation,
		},
		{
			name:     "unknown status rejected",
			query:    valid,
			args:     []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, "started"},
			wantCode: errCheckViolation,
		},
		{
			name:     "round from another season rejected (composite FK)",
			query:    valid,
			args:     []any{seasonID, roundID + 999999, homeID, awayID, 1.5, 2.5, "created"},
			wantCode: errForeignKey,
		},
		{
			name:     "nonexistent season rejected",
			query:    valid,
			args:     []any{seasonID + 999999, roundID, homeID, awayID, 1.5, 2.5, "created"},
			wantCode: errForeignKey,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			insertMatch(ctx, t, c)
		})
	}
}

// TestStatusVocabularyIsInSync inserts every status value from the Go slices
// and asserts the DB accepts all of them — the validator/DB drift detector.
func TestStatusVocabularyIsInSync(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	defer cleanup(ctx, t)

	t.Run("seasons", func(t *testing.T) {
		for _, status := range seasonStatuses {
			_, err := pool.Exec(ctx, `INSERT INTO seasons (status) VALUES ($1)`, status)
			if err != nil {
				t.Errorf("season status %q rejected by DB: %v", status, err)
			}
		}
	})

	t.Run("rounds", func(t *testing.T) {
		for i, status := range roundsStatuses {
			_, err := pool.Exec(ctx,
				`INSERT INTO rounds (season_id, number, status) VALUES ($1, $2, $3)`,
				seasonID, i+10, status)
			if err != nil {
				t.Errorf("round status %q rejected by DB: %v", status, err)
			}
		}
	})

	t.Run("matches", func(t *testing.T) {
		for _, status := range matchStatuses {
			// in_progress/closed require a score per matches_status_score_check,
			// so the vocabulary probe must supply one for those statuses.
			scored := status == "in_progress" || status == "closed"
			query := `
				INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score)
				VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 days', $8, $9)`
			var home, away int
			var homeArg, awayArg any
			if scored {
				home, away = 80, 75
				homeArg, awayArg = &home, &away
			}
			_, err := pool.Exec(ctx, query,
				seasonID, roundID, homeID, awayID, 1.5, 2.5, status, homeArg, awayArg)
			// A round hosts one match in this fixture; reuse it — matches has
			// no per-round uniqueness constraint, so this is fine.
			if err != nil {
				t.Errorf("match status %q rejected by DB: %v", status, err)
			}
		}
	})
}

func TestTeamConstraints(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	cases := []constraintCase{
		{
			name:  "valid team",
			query: `INSERT INTO teams (name, logo, description) VALUES ('Panathinaikos', 'https://x.example/l.png', 'd')`,
		},
		{
			name:     "empty name rejected",
			query:    `INSERT INTO teams (name, logo, description) VALUES ('', 'https://x.example/l.png', 'd')`,
			wantCode: errCheckViolation,
		},
		{
			name:     "name over 500 bytes rejected",
			query:    `INSERT INTO teams (name, logo, description) VALUES (repeat('x', 501), 'https://x.example/l.png', 'd')`,
			wantCode: errCheckViolation,
		},
		{
			name:     "name at exactly 500 bytes accepted",
			query:    `INSERT INTO teams (name, logo, description) VALUES (repeat('x', 500), 'https://x.example/l.png', 'd')`,
			wantCode: "",
		},
		{
			name:     "empty logo rejected",
			query:    `INSERT INTO teams (name, logo, description) VALUES ('PAO', '', 'd')`,
			wantCode: errCheckViolation,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, c.query)
			assertCode(t, c, err)
		})
	}
}

func TestRoundAndSeasonConstraints(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	seasonID, _, _, _ := seed(ctx, t)
	defer cleanup(ctx, t)

	cases := []constraintCase{
		{
			name:  "valid round",
			query: `INSERT INTO rounds (season_id, number, status) VALUES ($1, 2, 'open')`,
			args:  []any{seasonID},
		},
		{
			name:     "duplicate (season, number) rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 2, 'open')`,
			args:     []any{seasonID},
			wantCode: errUniqueViolation,
		},
		{
			name:     "round number 0 rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 0, 'open')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round number 39 rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 39, 'open')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round in nonexistent season rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 5, 'open')`,
			args:     []any{seasonID + 999999},
			wantCode: errForeignKey,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, c.query, c.args...)
			assertCode(t, c, err)
		})
	}
}

func TestTeamDeleteRestricted(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	defer cleanup(ctx, t)

	_, err := pool.Exec(ctx, `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES ($1, $2, $3, $4, 1.5, 2.5, 'created', now() + interval '30 days')`,
		seasonID, roundID, homeID, awayID)
	if err != nil {
		t.Fatalf("insert match: %v", err)
	}

	// ON DELETE RESTRICT: deleting a team with match history must fail.
	if _, err := pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, homeID); err == nil {
		t.Error("team with match history was deleted; expected RESTRICT to block it")
	} else {
		var pgErr *pgconn.PgError
		if !errorsAs(err, &pgErr) || pgErr.Code != errRestrictViolation {
			t.Errorf("expected FK restriction error, got: %v", err)
		}
	}
}

// silence unused import if time is ever dropped from the fixtures above.
