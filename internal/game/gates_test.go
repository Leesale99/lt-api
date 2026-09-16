package game

// Freeze-gate tests: verify the ADR-008 rule at the database level, where it
// is authoritative. The API layer can only show its own advisory checks;
// these tests exercise the triggers directly, the way every writer (API,
// seed, a future worker) hits them.
//
// "Started" is data, not clock: the gates key on starts_at <= now(), so each
// case controls starts_at instead of mocking time.

import (
	"context"
	"testing"
)

// errP0001 is the PostgreSQL code the ADR-007/ADR-008 gate triggers RAISE
// EXCEPTION with.
const errP0001 = "P0001"

// fixture holds the IDs of one freshly seeded hierarchy (1 season, 1 round,
// 2 teams).
type fixture struct {
	season, round, home, away int
}

// startedMatchSQL inserts a match that has already started and returns its id.
const startedMatchSQL = `
	INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at)
	VALUES ($1, $2, $3, $4, 1.5, 2.5, 50, 49, 'in_progress', now() - interval '1 hour')
	RETURNING id`

// futureMatchSQL inserts a match that has not started yet and returns its id.
const futureMatchSQL = `
	INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
	VALUES ($1, $2, $3, $4, 1.5, 2.5, 'created', now() + interval '1 hour')
	RETURNING id
`

func mustExec(ctx context.Context, t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func insertMatchRow(ctx context.Context, t *testing.T, query string, f fixture) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(ctx, query, f.season, f.round, f.home, f.away).Scan(&id); err != nil {
		t.Fatalf("insert match: %v", err)
	}
	return id
}

// gateCase is one trigger probe: optional prepare steps, then the statement
// under test. wantCode "" means the statement must succeed.
type gateCase struct {
	name     string
	prepare  func(ctx context.Context, t *testing.T, f fixture)
	query    string
	args     func(f fixture) []any
	wantCode string
}

func runGateCases(ctx context.Context, t *testing.T, cases []gateCase) {
	t.Helper()

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			cleanup(ctx, t)
			seasonID, roundID, homeID, awayID := seed(ctx, t)
			f := fixture{seasonID, roundID, homeID, awayID}

			if c.prepare != nil {
				c.prepare(ctx, t, f)
			}

			_, err := pool.Exec(ctx, c.query, c.args(f)...)
			assertCode(t, constraintCase{name: c.name, wantCode: c.wantCode}, err)
		})
	}
}

func TestMatchFreezeGate(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	runGateCases(ctx, t, []gateCase{
		{
			name: "started match cannot return to created",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `UPDATE matches SET status = 'created', home_score = NULL, away_score = NULL WHERE season_id = $1 AND round_id = $2`,
			args:     func(f fixture) []any { return []any{f.season, f.round} },
			wantCode: errP0001,
		},
		{
			name: "started match cannot be postponed",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `UPDATE matches SET status = 'postponed', home_score = NULL, away_score = NULL WHERE season_id = $1 AND round_id = $2`,
			args:     func(f fixture) []any { return []any{f.season, f.round} },
			wantCode: errP0001,
		},
		{
			name: "started match can close",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `UPDATE matches SET status = 'closed', home_score = 55, away_score = 60 WHERE season_id = $1 AND round_id = $2`,
			args:     func(f fixture) []any { return []any{f.season, f.round} },
			wantCode: "",
		},
		{
			name: "not-started match can be postponed",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, futureMatchSQL, f)
			},
			query:    `UPDATE matches SET status = 'postponed' WHERE season_id = $1 AND round_id = $2`,
			args:     func(f fixture) []any { return []any{f.season, f.round} },
			wantCode: "",
		},
		{
			name: "postponed match that has not started can return to created",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				id := insertMatchRow(ctx, t, futureMatchSQL, f)
				mustExec(ctx, t, `UPDATE matches SET status = 'postponed', starts_at = now() + interval '2 hours' WHERE id = $1`, id)
			},
			query:    `UPDATE matches SET status = 'created' WHERE season_id = $1 AND round_id = $2`,
			args:     func(f fixture) []any { return []any{f.season, f.round} },
			wantCode: "",
		},
	})
}

func TestRoundFreezeGate(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	runGateCases(ctx, t, []gateCase{
		{
			name: "round with a started match cannot return to open",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
				mustExec(ctx, t, `UPDATE rounds SET status = 'closed' WHERE id = $1`, f.round)
			},
			query:    `UPDATE rounds SET status = 'open' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: errP0001,
		},
		{
			name: "round with a started match cannot return to created",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
				mustExec(ctx, t, `UPDATE rounds SET status = 'closed' WHERE id = $1`, f.round)
			},
			query:    `UPDATE rounds SET status = 'created' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: errP0001,
		},
		{
			name: "round without started matches can regress from closed to open",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				mustExec(ctx, t, `UPDATE rounds SET status = 'closed' WHERE id = $1`, f.round)
			},
			query:    `UPDATE rounds SET status = 'open' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: "",
		},
		{
			name:     "open round without started matches can return to created",
			query:    `UPDATE rounds SET status = 'created' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: "",
		},
	})
}

func TestSeasonFreezeGate(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	runGateCases(ctx, t, []gateCase{
		{
			name: "season with a started match cannot return to open",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `UPDATE seasons SET status = 'open' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.season} },
			wantCode: errP0001,
		},
		{
			name: "season with a started match cannot return to created",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `UPDATE seasons SET status = 'created' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.season} },
			wantCode: errP0001,
		},
		{
			name:     "season without started matches can regress from in_progress to open",
			query:    `UPDATE seasons SET status = 'open' WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.season} },
			wantCode: "",
		},
	})
}

func TestSeasonDeleteGateRejectsMatchHistory(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	runGateCases(ctx, t, []gateCase{
		{
			name: "open season with a started match cannot be deleted",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				// Open first (no started matches yet, so the freeze gate allows
				// it), then insert the started match under the open season.
				mustExec(ctx, t, `UPDATE seasons SET status = 'open' WHERE id = $1`, f.season)
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `DELETE FROM seasons WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.season} },
			wantCode: errP0001,
		},
		{
			name: "open season without started matches can still be deleted",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				mustExec(ctx, t, `UPDATE seasons SET status = 'open' WHERE id = $1`, f.season)
			},
			query:    `DELETE FROM seasons WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.season} },
			wantCode: "",
		},
	})
}

func TestRoundDeleteGateRejectsMatchHistory(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	defer cleanup(ctx, t)

	runGateCases(ctx, t, []gateCase{
		{
			name: "open round with a started match cannot be deleted",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				// The seeded round is already open with no matches, so only the
				// started match makes it durable (ADR-008 consistency with the
				// seasons delete gate).
				insertMatchRow(ctx, t, startedMatchSQL, f)
			},
			query:    `DELETE FROM rounds WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: errP0001,
		},
		{
			name: "open round with a not-started match can still be deleted",
			prepare: func(ctx context.Context, t *testing.T, f fixture) {
				insertMatchRow(ctx, t, futureMatchSQL, f)
			},
			query:    `DELETE FROM rounds WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: "",
		},
		{
			name:     "open round without matches can still be deleted",
			query:    `DELETE FROM rounds WHERE id = $1`,
			args:     func(f fixture) []any { return []any{f.round} },
			wantCode: "",
		},
	})
}
