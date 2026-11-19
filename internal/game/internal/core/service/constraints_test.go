package service_test

// domain.Ride-gate constraint test: probes the declarative
// rides_money_state_coupling rule directly at the database, where it is
// the enforcement home (see testmain_test.go for the harness). The
// calendar-entity constraint drift tests live in internal/schedule.
//
// Requires a running PostgreSQL. LT_API_TEST_DSN points at the test database
// (see internal/testdb for the setup contract), which the harness creates and
// drops itself — the test role needs only LOGIN + CREATEDB.
//
// Without LT_API_TEST_DSN the suite fails, unless AUDIT_SKIP_DB=1 opts out
// explicitly; the DB-backed tests then skip, saying so.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/adapter/outbound/postgres"
	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/testdb"
)

// migFile lists all migrations the game tests need — the FULL series
// since 000015: that migration moves whatever tables exist (it cannot run
// against a partial chain), 000016–000018 (FK surgery, roles) assume it,
// and the suite runs its stores on the role-restricted pool 000018
// creates. 000001 creates the base hierarchy, 000005 adds
// matches.ended_at — the calendar's game-facing phase read uses it, so every
// ride use case depends on it.
var migFiles = []string{
	"../../../../../migrations/000001_create_initial_game_models.up.sql",
	"../../../../../migrations/000002_create_users_table.up.sql",
	"../../../../../migrations/000003_create_user_tokens_table.up.sql",
	"../../../../../migrations/000004_add_permissions.up.sql",
	"../../../../../migrations/000005_add_matches_ended_at.up.sql",
	// domain.Ride persistence: phase 03 replaced the in-memory ride stub with the
	// rides table — the service ride tests plant rides into it directly.
	"../../../../../migrations/000006_create_rides_table.up.sql",
	"../../../../../migrations/000007_alter_matches_odds_precision.up.sql",
	// Round lifecycle gates (000008): rounds_close_gate is the backstop the
	// round-close transaction tests refuse against, and rounds_progress_gate
	// participates in the mid-transaction-failure case (it fires on the
	// status flip after the ride resolution, making the whole tx abort).
	"../../../../../migrations/000008_add_round_lifecycle_gates.up.sql",
	"../../../../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
	// Idempotency keys (000010): the idempotent lock service tests claim
	// and replay through the real table.
	"../../../../../migrations/000010_add_idempotency_keys_table.up.sql",
	// Result agreement (000011): superseded by 000017 (the app-level
	// check) but part of the chain the schema is built from.
	"../../../../../migrations/000011_add_rides_result_agreement_gate.up.sql",
	// INSERT state gate (000012): rides are born locked only — the plant
	// helpers walk to other states through the UPDATE gate.
	"../../../../../migrations/000012_add_rides_insert_state_gate.up.sql",
	// Money-state coupling (000013): the declarative CHECK that took
	// trigger check 4's job — unlocked|lost arrivals carry no acc. The
	// UPDATE gate body runs without check 4 from here on.
	"../../../../../migrations/000013_add_rides_money_state_coupling.up.sql",
	// Relaxed close gate (000014): since the event seam the round
	// close publishes RoundCompleted and the ride settlement is the game
	// context's own transaction — undecided rides no longer refuse a close.
	"../../../../../migrations/000014_relax_rounds_close_gate.up.sql",
	// 000015–000018: the context schemas, the FK/trigger
	// surgery and the per-context roles — the game stores run on the
	// role-restricted pool these create.
	"../../../../../migrations/000015_split_context_schemas.up.sql",
	"../../../../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
	"../../../../../migrations/000017_app_level_result_agreement.up.sql",
	"../../../../../migrations/000018_context_roles.up.sql",
	// 000019-000021: the full production chain continues — the status-rank
	// gates, the ended_at stamp that keeps a supplied end time, and the
	// game-side definer delete.
	"../../../../../migrations/000019_add_status_rank_gates.up.sql",
	"../../../../../migrations/000020_respect_supplied_match_ended_at.up.sql",
	"../../../../../migrations/000021_add_game_delete_players_for_season.up.sql",
	"../../../../../migrations/000022_scope_idempotency_keys_per_caller.up.sql",
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

// rideRepo and transactor are the game storage adapters the harness plants
// fixtures through (the same constructors the module facade wires).
var (
	rideRepo   *postgres.RideRepository
	transactor postgres.Transactor
)

// TestMain always runs the full suite: unit tests in this package must run
// on a DB-less machine too. Only the DB-backed tests skip, one by one, via
// requireDB — and only under the explicit AUDIT_SKIP_DB=1 opt-out.
// Exiting before m.Run() would silently disable the whole package — including
// `go test -list`.
func TestMain(m *testing.M) {
	dsn := os.Getenv(testdb.DSNEnv)
	switch decision := testdb.DecideSkip(dsn, os.Getenv(testdb.SkipOptOutEnv)); decision.Action {
	case testdb.ActionFail:
		// Not a skip: the DB-backed half did not run, so the exit status says
		// so. The message carries the way out.
		fmt.Fprintln(os.Stderr, decision.Message)
		os.Exit(1)
	case testdb.ActionSkip:
		testdb.Announce(decision.Message)
		os.Exit(m.Run())
	}

	ctx := context.Background()

	// Fresh database per run: the migration itself is under test.
	db, teardown, err := testdb.Setup(ctx, dsn, "game_service", migFiles...)
	if err != nil {
		// DSN set but unusable: fail loudly rather than report a green run
		// that tested nothing.
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	pool = db.Owner
	gamePool = db.Game
	schedulePool = db.Schedule
	rideRepo = postgres.NewRideRepository(gamePool)
	transactor = postgres.NewTransactor(gamePool)

	code := m.Run()
	teardown()
	os.Exit(code)
}

// requireDB skips a test under the explicit AUDIT_SKIP_DB=1 opt-out (TestMain
// leaves pool nil there); otherwise TestMain has already failed or built it.
func requireDB(t *testing.T) {
	t.Helper()
	if pool == nil {
		t.Skip("database skipped: AUDIT_SKIP_DB=1 opted out")
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

// wantCode: "" means the statement must succeed; otherwise the pg error code expected.
type constraintCase struct {
	name     string
	wantCode string
}

// errorsAs delegates to errors.As for *pgconn.PgError.
func errorsAs(err error, target **pgconn.PgError) bool {
	return errors.As(err, target)
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

// TestRideMoneyStateCoupling probes the declarative gate that took
// trigger check 4's job (000013): unlocked|lost arrivals must carry no
// accumulated bonus_acc, burned keeps the value it accrued (what ride.Burn
// banks on). Direct SQL probes, not the domain: the rule's enforcement
// home is the constraint itself. The refusals must name
// rides_money_state_coupling — that is the name the store maps to
// domain.ErrInvalidTransition, and hard-coding it here keeps the test, the
// constraint and ridesMoneyStateCoupling (the postgres adapter's ride_repo.go)
// in
// lockstep.
func TestRideMoneyStateCoupling(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	defer cleanup(ctx, t)

	constraintName := "rides_money_state_coupling"

	matchID := insertClosedMatchScored(ctx, t, seasonID, roundID, homeID, awayID, 2, 1)

	cases := []struct {
		name     string
		state    string
		acc      string
		wantCode string
	}{
		{"lost arrival with acc = 0 passes", "lost", "0", ""},
		{"unlocked arrival with acc = 0 passes", "unlocked", "0", ""},
		{"burned keeps the acc it accrued (not a money-state arrival)", "burned", "100", ""},
		{"lost arrival carrying acc refused", "lost", "100", errCheckViolation},
		{"unlocked arrival carrying acc refused", "unlocked", "100", errCheckViolation},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			// unlocked|burned arrive from won_pending only (UPDATE gate
			// check 3), and locked → won_pending requires the ride's team
			// to have won (check 5) — so the probing ride departs home-side
			// through the walk, while lost is reached from locked away-side
			// (the home team won 2–1). Locked rides may carry acc (only
			// unlocked|lost arrivals are coupled to zero), so the probe
			// plants one holding 100 and attempts the arrival in one
			// statement — the same shape plantRide's walk (service_test.go)
			// and the calendar's auto-unlock statement use.
			team := awayID
			if tt.state != "lost" {
				team = homeID
			}
			ride := plantRide(ctx, t, matchID, team, domain.RideLocked, decimal.NewFromInt(100), 0)

			if tt.state != "lost" {
				if _, err := pool.Exec(ctx, `UPDATE game.rides SET state = 'won_pending' WHERE id = $1`, ride.ID); err != nil {
					t.Fatalf("walk locked → won_pending: %v", err)
				}
			}

			_, err := pool.Exec(ctx,
				`UPDATE game.rides SET state = $2, bonus_acc = $3 WHERE id = $1`,
				ride.ID, tt.state, tt.acc)
			assertCode(t, constraintCase{name: tt.name, wantCode: tt.wantCode}, err)

			if tt.wantCode == "" {
				return
			}

			// The refusal must be the money-state constraint by name, not
			// just any 23514: the rides table carries five other CHECKs and
			// the store maps only this one to domain.ErrInvalidTransition.
			var pgErr *pgconn.PgError
			if errorsAs(err, &pgErr) && pgErr.ConstraintName != constraintName {
				t.Errorf("refusal named constraint %q, want %q", pgErr.ConstraintName, constraintName)
			}
		})
	}
}
