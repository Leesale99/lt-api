package postgres

// The Postgres adapter's DB harness: a fresh migrated database per run, the
// three context pools the composition root also opens, and the fixture
// planters the ride-repo tests share. The service-level harness lives in the
// use-case package; each package owns its own TestMain (Go allows one per
// test binary), so the shared fixture helpers are deliberately duplicated —
// the same shape internal/schedule's repository and service packages use.
//
// Requires a running PostgreSQL. LT_API_TEST_DSN (see .envrc) points at the
// test database, which the harness creates and drops itself — the test role
// needs only LOGIN + CREATEDB (see internal/testdb).
//
// Without LT_API_TEST_DSN the tests are skipped.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/adapter/outbound"
	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/testdb"
)

// migFile lists all migrations the game tests need — the FULL series
// since 000015: that migration moves whatever tables exist (it cannot run
// against a partial chain), 000016–000018 (FK surgery, roles) assume it,
// and the suite runs its stores on the role-restricted pool 000018
// creates. 000001 creates the base hierarchy, 000005 adds
// matches.ended_at — phase derivation reads it via
// the calendar's game-facing read, so every ride use case depends on it.
var migFiles = []string{
	"../../../../../../migrations/000001_create_initial_game_models.up.sql",
	"../../../../../../migrations/000002_create_users_table.up.sql",
	"../../../../../../migrations/000003_create_user_tokens_table.up.sql",
	"../../../../../../migrations/000004_add_permissions.up.sql",
	"../../../../../../migrations/000005_add_matches_ended_at.up.sql",
	// Ride persistence: phase 03 replaced the in-memory ride stub with the
	// rides table — the service ride tests plant rides into it directly.
	"../../../../../../migrations/000006_create_rides_table.up.sql",
	"../../../../../../migrations/000007_alter_matches_odds_precision.up.sql",
	// Round lifecycle gates (000008): rounds_close_gate is the backstop the
	// round-close transaction tests refuse against, and rounds_progress_gate
	// participates in the mid-transaction-failure case (it fires on the
	// status flip after the ride resolution, making the whole tx abort).
	"../../../../../../migrations/000008_add_round_lifecycle_gates.up.sql",
	"../../../../../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
	// Idempotency keys (000010): the idempotent lock service tests claim
	// and replay through the real table.
	"../../../../../../migrations/000010_add_idempotency_keys_table.up.sql",
	// Result agreement (000011): superseded by 000017 (the app-level
	// check) but part of the chain the schema is built from.
	"../../../../../../migrations/000011_add_rides_result_agreement_gate.up.sql",
	// INSERT state gate (000012): rides are born locked only — the plant
	// helpers walk to other states through the UPDATE gate.
	"../../../../../../migrations/000012_add_rides_insert_state_gate.up.sql",
	// Money-state coupling (000013): the declarative CHECK that took
	// trigger check 4's job — unlocked|lost arrivals carry no acc. The
	// UPDATE gate body runs without check 4 from here on.
	"../../../../../../migrations/000013_add_rides_money_state_coupling.up.sql",
	// Relaxed close gate (000014): since the event seam the round
	// close publishes RoundCompleted and the ride settlement is the game
	// context's own transaction — undecided rides no longer refuse a close.
	"../../../../../../migrations/000014_relax_rounds_close_gate.up.sql",
	// 000015–000018: the context schemas, the FK/trigger
	// surgery and the per-context roles — the game stores run on the
	// role-restricted pool these create.
	"../../../../../../migrations/000015_split_context_schemas.up.sql",
	"../../../../../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
	"../../../../../../migrations/000017_app_level_result_agreement.up.sql",
	"../../../../../../migrations/000018_context_roles.up.sql",
}

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

// gamePool runs as game_app: the stores under test exercise the
// real privilege regime; pool (owner) plants fixtures — including
// cross-schema ones (matches/rounds/teams are the schedule context's).
var gamePool *pgxpool.Pool

// schedulePool runs as schedule_app: the SchedulePort half of the service
// tests (the resolve/close scripts and the calendar reads) run on the
// schedule context's own pool, the composition shape.
var schedulePool *pgxpool.Pool

// rideRepo is the adapter under test; schedACL is the calendar port the
// decision-gate helper drives, wired exactly as the module facade wires it.
var (
	rideRepo *RideRepository
	schedACL port.Schedule
)

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
	db, teardown, err := testdb.Setup(ctx, dsn, "game_repository", migFiles...)
	if err != nil {
		// DSN set but unusable: fail loudly rather than report a green run
		// that tested nothing.
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	pool = db.Owner
	gamePool = db.Game
	schedulePool = db.Schedule
	rideRepo = NewRideRepository(gamePool)
	schedACL = outbound.NewScheduleACL(schedule.NewModule(schedulePool, nil, nil))

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

// seedFixture re-seeds the base hierarchy (open round 1, teams 1 & 2) and
// returns their IDs; cleanup first, so IDs restart from a known point.
func seedFixture(ctx context.Context, t *testing.T) (roundID, homeID, awayID int) {
	t.Helper()

	cleanup(ctx, t)
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	_ = seasonID

	return roundID, homeID, awayID
}

// insertFutureMatch adds a pre-start match starting `lead` from now and
// returns its id. status is created; no score, no ended_at.
func insertFutureMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, lead string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 'created', now() + $5::interval)
		RETURNING id
	`, seasonID, roundID, homeID, awayID, lead).Scan(&id)
	if err != nil {
		t.Fatalf("insert future match: %v", err)
	}

	return id
}

// insertClosedMatch plants a closed, scored match that ended `ended` ago
// (started three hours before it ended).
func insertClosedMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, ended string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 88, 79, 'closed', now() - $5::interval - interval '3 hours', now() - $5::interval)
		RETURNING id
	`, seasonID, roundID, homeID, awayID, ended).Scan(&id)
	if err != nil {
		t.Fatalf("insert closed match: %v", err)
	}

	return id
}

// insertClosedMatchScored plants a closed match carrying the given final
// scores (insertClosedMatch hardcodes 88/79; the rule fixture needs
// arbitrary vectors).
func insertClosedMatchScored(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID, home, away int) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, $5, $6, 'closed', now() - interval '5 hours', now() - interval '2 hours')
		RETURNING id
	`, seasonID, roundID, homeID, awayID, home, away).Scan(&id)
	if err != nil {
		t.Fatalf("insert scored closed match: %v", err)
	}

	return id
}

// plantPlayer inserts a player row on the fixture season (id restarts to 1
// on cleanup) and returns the new player's id.
func plantPlayer(ctx context.Context, t *testing.T, teamID int) int {
	t.Helper()

	var playerID int
	err := pool.QueryRow(ctx, `
		INSERT INTO game.players (season_id, favorite_team_id, name)
		VALUES (1, $1, 'ride player')
		RETURNING id
	`, teamID).Scan(&playerID)
	if err != nil {
		t.Fatalf("plant player: %v", err)
	}

	return playerID
}

// plantRide stores a ride in the given state/acc/streak through the real
// INSERT plus a gated walk to the requested state (since 000012 the INSERT
// gate admits locked only). The walk's own transitions are incidental to what
// tests arrange, so the final UPDATE resets version to 1.
func plantRide(ctx context.Context, t *testing.T, matchID, teamID int, state domain.RideState, acc decimal.Decimal, streak int) domain.Ride {
	t.Helper()

	ride, err := rideRepo.Insert(ctx, domain.Ride{
		PlayerID:     plantPlayer(ctx, t, teamID),
		TeamID:       teamID,
		MatchID:      matchID,
		State:        domain.RideLocked,
		TokensLocked: decimal.NewFromInt(100),
		BaseAtLock:   decimal.NewFromInt(100),
		Acc:          acc,
		Streak:       streak,
	})
	if err != nil {
		t.Fatalf("plant ride: %v", err)
	}

	if state != domain.RideLocked {
		// burned/unlocked arrive only via won_pending (gate check 3).
		if state == domain.RideBurned || state == domain.RideUnlocked {
			if _, err := pool.Exec(ctx, `UPDATE game.rides SET state = 'won_pending' WHERE id = $1`, ride.ID); err != nil {
				t.Fatalf("plant ride walk (locked -> won_pending): %v", err)
			}
		}

		if _, err := pool.Exec(ctx, `
			UPDATE game.rides SET state = $2, bonus_acc = $3, version = 1 WHERE id = $1
		`, ride.ID, state, acc); err != nil {
			t.Fatalf("plant ride walk (-> %s): %v", state, err)
		}

		ride.State = state
		ride.Acc = acc
		ride.Version = 1
	}

	return ride
}
