package schedule

// Constraint tests: verify that the database schema (migrations/) enforces
// the same rules as the Go validators, and that the permitted status values
// in the Go slices are accepted by the DB CHECKs (drift detection).
// Requires a running PostgreSQL (see testmain_test.go).

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// errorsAs delegates to errors.As for *pgconn.PgError.
func errorsAs(err error, target **pgconn.PgError) bool {
	return errors.As(err, target)
}

func insertMatch(ctx context.Context, t *testing.T, c constraintCase) {
	t.Helper()
	_, err := pool.Exec(ctx, c.query, c.args...)
	assertCode(t, c, err)
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
			// Migration 000005: a closed match must carry ended_at (the
			// matches_ended_at_check invariant). The insert sets it explicitly —
			// the ended-at trigger only fires on UPDATE, not INSERT.
			name:  "closed match with score",
			query: fmt.Sprintf(base, ", home_score, away_score, ended_at", ", $8, $9, now() + interval '31 days'"),
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
			query:    fmt.Sprintf(base, ", home_score, away_score, ended_at", ", $8, $9, now() + interval '31 days'"),
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
		for _, status := range roundStatuses {
			// Each vocabulary value gets its own live season: the lifecycle
			// gates (000008) refuse a round beyond created in a season that is
			// not open/in_progress, and number 1 is the only round a fresh
			// season accepts — so this is the one shape that admits every
			// vocabulary value for a direct INSERT.
			var sid int
			if err := pool.QueryRow(ctx, `INSERT INTO seasons (status) VALUES ('in_progress') RETURNING id`).Scan(&sid); err != nil {
				t.Fatalf("seed season for round status %q: %v", status, err)
			}
			_, err := pool.Exec(ctx,
				`INSERT INTO rounds (season_id, number, status) VALUES ($1, 1, $2)`,
				sid, status)
			if err != nil {
				t.Errorf("round status %q rejected by DB: %v", status, err)
			}
		}
	})

	t.Run("matches", func(t *testing.T) {
		for _, status := range matchStatuses {
			// in_progress/closed require a score per matches_status_score_check,
			// so the vocabulary probe must supply one for those statuses; closed
			// also requires ended_at (migration 000005, matches_ended_at_check).
			scored := status == "in_progress" || status == "closed"
			query, arg := `
				INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score)
				VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 days', $8, $9)`, []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, status, nil, nil}
			var home, away int
			if scored {
				home, away = 80, 75
				arg[7], arg[8] = home, away
			}
			if status == "closed" {
				// The ended-at trigger fires on UPDATE only; INSERT supplies it.
				query = `
					INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score, ended_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 days', $8, $9, now() + interval '31 days')`
			}
			_, err := pool.Exec(ctx, query, arg...)
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

	// Rounds are inserted as 'created' so the constraint under test is what
	// fires: the lifecycle gates (000008) run BEFORE the constraints and
	// would mask a CHECK/UNIQUE/FK violation with P0001 on an 'open' insert.
	cases := []constraintCase{
		{
			name:  "valid round",
			query: `INSERT INTO rounds (season_id, number, status) VALUES ($1, 2, 'created')`,
			args:  []any{seasonID},
		},
		// A second distinct (season, number) pair: the duplicate case below
		// refuses number 2 on an already-planted round 2, not on its own insert.
		{
			name:  "second created round at its own number",
			query: `INSERT INTO rounds (season_id, number, status) VALUES ($1, 3, 'created')`,
			args:  []any{seasonID},
		},
		{
			name:     "duplicate (season, number) rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 2, 'created')`,
			args:     []any{seasonID},
			wantCode: errUniqueViolation,
		},
		{
			name:     "round number 0 rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 0, 'created')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round number 39 rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 39, 'created')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round in nonexistent season rejected",
			query:    `INSERT INTO rounds (season_id, number, status) VALUES ($1, 5, 'created')`,
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
