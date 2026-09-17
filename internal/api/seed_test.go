package api

// Canonical fixture: after reset(), every table is empty and re-seeded with
// fixed IDs (TRUNCATE ... RESTART IDENTITY), so all handler tests can address
// rows by ID — team 1 is always Olympiacos, match 1 is always the closed one.
// Tests needing extra rows (e.g. a second season for the wrong-round case)
// insert them via the seed helpers and use the returned IDs.

import (
	"context"
	"testing"
)

const resetSQL = `
	TRUNCATE matches, players, rounds, seasons, teams, users RESTART IDENTITY CASCADE;

	INSERT INTO teams (name, logo, description) VALUES
		('Olympiacos', 'https://x.example/oly.png', 'Piraeus'),
		('Real Madrid', 'https://x.example/rm.png', 'Madrid');

	INSERT INTO seasons (status) VALUES
		('closed'),      -- id 1
		('in_progress'); -- id 2

	INSERT INTO rounds (season_id, number, status) VALUES
		(1, 1, 'open'),  -- id 1: hosts the canonical matches
		(1, 2, 'open'),  -- id 2
		(2, 1, 'open');  -- id 3: round in season 2 (wrong-season cases)

	INSERT INTO players (season_id, favorite_team_id, name) VALUES
		(2, 1, 'Sasha Vezenkov');

	INSERT INTO matches (
		season_id, round_id, home_team_id, away_team_id,
		home_odds, away_odds, home_score, away_score, status, starts_at
	) VALUES
		(1, 1, 1, 2, 1.5, 2.5, 88, 79, 'closed', now() - interval '7 days'),
		(1, 1, 2, 1, 2.0, 1.8, NULL, NULL, 'created', now() + interval '7 days');
`

// reset wipes all tables and re-seeds the canonical fixture. Call it at the
// top of every subtest: fresh state per subtest, same IDs every time.
func reset(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), resetSQL); err != nil {
		t.Fatalf("reset seed fixture: %v", err)
	}
}
