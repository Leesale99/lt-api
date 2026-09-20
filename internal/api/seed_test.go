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

// roles, permissions and roles_permissions are reference data seeded by
// migration 000004 and never mutated by any handler, so reset() deliberately
// leaves them in place. Everything else is truncated and re-seeded with fixed
// IDs.
const resetSQL = `
	TRUNCATE user_tokens, users, matches, players, rounds, seasons, teams
	RESTART IDENTITY;

	INSERT INTO users (name, email, password_hash, activated, role_id) VALUES
		('Admin',   'admin@example.com',   '$2a$12$wKTvXl0oSi7VojP83AVHGeYIdbBV.kay9a.I5U3hIfJ.jWdrVBBv6', true,  (SELECT id FROM roles WHERE name = 'admin')),   -- id 1
		('Regular', 'user@example.com',    '$2a$12$wKTvXl0oSi7VojP83AVHGeYIdbBV.kay9a.I5U3hIfJ.jWdrVBBv6', true,  (SELECT id FROM roles WHERE name = 'user')),    -- id 2
		('Pending', 'pending@example.com', '$2a$12$wKTvXl0oSi7VojP83AVHGeYIdbBV.kay9a.I5U3hIfJ.jWdrVBBv6', false, (SELECT id FROM roles WHERE name = 'user'));    -- id 3

	-- All four password hashes above are bcrypt("pa55word123"). The token
	-- plaintexts these rows pair with live in auth_test.go; expiry is an hour,
	-- far beyond any single test run, except the deliberately expired one.
	INSERT INTO user_tokens (hash, user_id, expiry, scope) VALUES
		(decode('9976d549a25115dab4e36d0c1fb8f31cb07da87dd83275977360eb7dc09e88de', 'hex'), 1, now() + interval '1 hour', 'authentication'),  -- admin user
		(decode('c9ca6164dd79234f6dd18075205c763bb5930efdc0026b46157f8a75ab44fbdc', 'hex'), 2, now() + interval '1 hour', 'authentication'),  -- activated 'user'
		(decode('72e7fd18f3ce18987d9c95e4454e466148ab18d000cc23d7ae11afc0815edc21', 'hex'), 3, now() + interval '1 hour', 'authentication'),  -- unactivated 'user'
		(decode('5c637d48cd349889246bc9f73fcdaddafeca49ba9df0ec4551b02cf705a493e6', 'hex'), 1, now() - interval '1 minute', 'authentication'); -- expired

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
