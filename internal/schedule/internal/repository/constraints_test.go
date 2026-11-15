package repository

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

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
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
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at%s)
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
		for _, status := range model.SeasonStatuses {
			_, err := pool.Exec(ctx, `INSERT INTO schedule.seasons (status) VALUES ($1)`, status)
			if err != nil {
				t.Errorf("season status %q rejected by DB: %v", status, err)
			}
		}
	})

	t.Run("rounds", func(t *testing.T) {
		for _, status := range model.RoundStatuses {
			// Each vocabulary value gets its own live season: the lifecycle
			// gates (000008) refuse a round beyond created in a season that is
			// not open/in_progress, and number 1 is the only round a fresh
			// season accepts — so this is the one shape that admits every
			// vocabulary value for a direct INSERT.
			var sid int
			if err := pool.QueryRow(ctx, `INSERT INTO schedule.seasons (status) VALUES ('in_progress') RETURNING id`).Scan(&sid); err != nil {
				t.Fatalf("seed season for round status %q: %v", status, err)
			}
			_, err := pool.Exec(ctx,
				`INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 1, $2)`,
				sid, status)
			if err != nil {
				t.Errorf("round status %q rejected by DB: %v", status, err)
			}
		}
	})

	t.Run("matches", func(t *testing.T) {
		for _, status := range model.MatchStatuses {
			// in_progress/closed require a score per matches_status_score_check,
			// so the vocabulary probe must supply one for those statuses; closed
			// also requires ended_at (migration 000005, matches_ended_at_check).
			scored := status == "in_progress" || status == "closed"
			query, arg := `
				INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score)
				VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '30 days', $8, $9)`, []any{seasonID, roundID, homeID, awayID, 1.5, 2.5, status, nil, nil}
			var home, away int
			if scored {
				home, away = 80, 75
				arg[7], arg[8] = home, away
			}
			if status == "closed" {
				// The ended-at trigger fires on UPDATE only; INSERT supplies it.
				query = `
					INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score, ended_at)
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

// TestStatusRankGatesMirrorGoRanks is the rank drift detector: for every
// ordered status pair of every schedule vocabulary it probes the DB gate with
// a fresh minimal fixture and asserts the gate refuses exactly the pairs
// model.XStatusRegresses calls regressions — with the rank gate's own
// message, so a refusal from a neighbouring gate (started-freeze, sequence,
// season-live) cannot stand in for the rank rule. Migration 000019 mirrors
// the Go rank maps; change one, change both.
func TestStatusRankGatesMirrorGoRanks(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	cleanup(ctx, t)
	defer cleanup(ctx, t)

	// newCalendar plants the one shape that isolates the rank rule from the
	// neighbouring round gates: a fresh live season (no
	// round_progress_season_not_live) holding only round 1 (max closed round =
	// 0 = 1-1, no round_sequence_incomplete).
	newCalendar := func(t *testing.T, roundStatus model.RoundStatus) (seasonID, roundID int) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`INSERT INTO schedule.seasons (status) VALUES ('in_progress') RETURNING id`).Scan(&seasonID); err != nil {
			t.Fatalf("plant live season: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 1, $2) RETURNING id`,
			seasonID, roundStatus).Scan(&roundID); err != nil {
			t.Fatalf("plant round 1 as %s: %v", roundStatus, err)
		}
		return seasonID, roundID
	}

	t.Run("matches", func(t *testing.T) {
		// Teams only: seed's calendar rows are unused, every probe plants its
		// own season + round.
		_, _, homeID, awayID := seed(ctx, t)

		for _, oldStatus := range model.MatchStatuses {
			for _, newStatus := range model.MatchStatuses {
				t.Run(fmt.Sprintf("%s_to_%s", oldStatus, newStatus), func(t *testing.T) {
					seasonID, roundID := newCalendar(t, model.RoundCreated)

					// "Started" is the time fact the started-freeze rule keys on,
					// and it must not decide this probe: a future starts_at
					// isolates the rank rule — except when the target is closed,
					// where the 000005 ended_at stamp (now()) only stays after
					// starts_at if the match has already started.
					started := oldStatus == model.MatchInProgress || oldStatus == model.MatchClosed || newStatus == model.MatchClosed
					startsAt := "now() + interval '1 day'"
					if started {
						startsAt = "now() - interval '2 days'"
					}

					// Old-row shape per matches_status_score_check (score exactly
					// when in_progress/closed) and matches_ended_at_check
					// (ended_at exactly when closed, after starts_at).
					var homeScore, awayScore any
					endedAt := "NULL"
					if oldStatus == model.MatchInProgress || oldStatus == model.MatchClosed {
						homeScore, awayScore = 80, 75
					}
					if oldStatus == model.MatchClosed {
						endedAt = "now() - interval '2 days' + interval '1 hour'"
					}

					var matchID int
					err := pool.QueryRow(ctx, fmt.Sprintf(`
						INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at, home_score, away_score, ended_at)
						VALUES ($1, $2, $3, $4, 1.5, 2.5, $5, %s, $6, $7, %s)
						RETURNING id`, startsAt, endedAt),
						seasonID, roundID, homeID, awayID, oldStatus, homeScore, awayScore).Scan(&matchID)
					if err != nil {
						t.Fatalf("plant %s match: %v", oldStatus, err)
					}

					// New-row shape for the target: score for in_progress/closed;
					// ended_at kept for closed (the 000005 stamp supplies it on the
					// transition) and cleared otherwise.
					var newHome, newAway any
					if newStatus == model.MatchInProgress || newStatus == model.MatchClosed {
						newHome, newAway = 80, 75
					}
					_, err = pool.Exec(ctx, `
						UPDATE schedule.matches
						SET status = $2, home_score = $3, away_score = $4,
						    ended_at = CASE WHEN $2::text = 'closed' THEN ended_at ELSE NULL END
						WHERE id = $1`, matchID, newStatus, newHome, newAway)

					assertRankGate(t, err, model.MatchStatusRegresses(oldStatus, newStatus), "match_status_regression")
				})
			}
		}
	})

	t.Run("rounds", func(t *testing.T) {
		for _, oldStatus := range model.RoundStatuses {
			for _, newStatus := range model.RoundStatuses {
				t.Run(fmt.Sprintf("%s_to_%s", oldStatus, newStatus), func(t *testing.T) {
					// Planted directly in its old status: round 1 of a live season
					// is admissible for every vocabulary value (the progress gate
					// lets an INSERT through once season-live and the sequence
					// hold), so no other gate is set up to refuse a legal pair.
					_, roundID := newCalendar(t, oldStatus)

					_, err := pool.Exec(ctx, `UPDATE schedule.rounds SET status = $2 WHERE id = $1`, roundID, newStatus)
					assertRankGate(t, err, model.RoundStatusRegresses(oldStatus, newStatus), "round_status_regression")
				})
			}
		}
	})

	t.Run("seasons", func(t *testing.T) {
		for _, oldStatus := range model.SeasonStatuses {
			for _, newStatus := range model.SeasonStatuses {
				t.Run(fmt.Sprintf("%s_to_%s", oldStatus, newStatus), func(t *testing.T) {
					// Seasons have no INSERT gate, so the fixture row can be
					// planted in any status; with no matches, season-live is the
					// only neighbouring check and it never refuses here.
					var seasonID int
					if err := pool.QueryRow(ctx,
						`INSERT INTO schedule.seasons (status) VALUES ($1) RETURNING id`,
						oldStatus).Scan(&seasonID); err != nil {
						t.Fatalf("plant %s season: %v", oldStatus, err)
					}

					_, err := pool.Exec(ctx, `UPDATE schedule.seasons SET status = $2 WHERE id = $1`, seasonID, newStatus)
					assertRankGate(t, err, model.SeasonStatusRegresses(oldStatus, newStatus), "season_status_regression")
				})
			}
		}
	})
}

// assertRankGate pins one probe outcome against the Go rank oracle: a
// regression must be refused by the rank rule itself (P0001 carrying the
// entity's distinctive message), and every other ordered pair must be
// accepted — the fixtures above are shaped to isolate the rank rule, so a
// coincidental refusal from another gate fails here.
func assertRankGate(t *testing.T, err error, regresses bool, message string) {
	t.Helper()

	if !regresses {
		if err != nil {
			t.Fatalf("rank-legal status pair refused: %v", err)
		}
		return
	}

	if err == nil {
		t.Fatalf("rank regression accepted; want P0001 (%s)", message)
	}
	var pgErr *pgconn.PgError
	if !errorsAs(err, &pgErr) {
		t.Fatalf("want pg error P0001 (%s), got %T %v", message, err, err)
	}
	if pgErr.Code != errP0001 || pgErr.Message != message {
		t.Fatalf("got %s (%s), want P0001 (%s)", pgErr.Code, pgErr.Message, message)
	}
}

func TestTeamConstraints(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	cases := []constraintCase{
		{
			name:  "valid team",
			query: `INSERT INTO schedule.teams (name, logo, description) VALUES ('Panathinaikos', 'https://x.example/l.png', 'd')`,
		},
		{
			name:     "empty name rejected",
			query:    `INSERT INTO schedule.teams (name, logo, description) VALUES ('', 'https://x.example/l.png', 'd')`,
			wantCode: errCheckViolation,
		},
		{
			name:     "name over 500 bytes rejected",
			query:    `INSERT INTO schedule.teams (name, logo, description) VALUES (repeat('x', 501), 'https://x.example/l.png', 'd')`,
			wantCode: errCheckViolation,
		},
		{
			name:     "name at exactly 500 bytes accepted",
			query:    `INSERT INTO schedule.teams (name, logo, description) VALUES (repeat('x', 500), 'https://x.example/l.png', 'd')`,
			wantCode: "",
		},
		{
			name:     "empty logo rejected",
			query:    `INSERT INTO schedule.teams (name, logo, description) VALUES ('PAO', '', 'd')`,
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
			query: `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 2, 'created')`,
			args:  []any{seasonID},
		},
		// A second distinct (season, number) pair: the duplicate case below
		// refuses number 2 on an already-planted round 2, not on its own insert.
		{
			name:  "second created round at its own number",
			query: `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 3, 'created')`,
			args:  []any{seasonID},
		},
		{
			name:     "duplicate (season, number) rejected",
			query:    `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 2, 'created')`,
			args:     []any{seasonID},
			wantCode: errUniqueViolation,
		},
		{
			name:     "round number 0 rejected",
			query:    `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 0, 'created')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round number 39 rejected",
			query:    `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 39, 'created')`,
			args:     []any{seasonID},
			wantCode: errCheckViolation,
		},
		{
			name:     "round in nonexistent season rejected",
			query:    `INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 5, 'created')`,
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
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES ($1, $2, $3, $4, 1.5, 2.5, 'created', now() + interval '30 days')`,
		seasonID, roundID, homeID, awayID)
	if err != nil {
		t.Fatalf("insert match: %v", err)
	}

	// ON DELETE RESTRICT: deleting a team with match history must fail.
	if _, err := pool.Exec(ctx, `DELETE FROM schedule.teams WHERE id = $1`, homeID); err == nil {
		t.Error("team with match history was deleted; expected RESTRICT to block it")
	} else {
		var pgErr *pgconn.PgError
		if !errorsAs(err, &pgErr) || pgErr.Code != errRestrictViolation {
			t.Errorf("expected FK restriction error, got: %v", err)
		}
	}
}
