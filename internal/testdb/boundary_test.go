package testdb

// Boundary smoke test: the context privilege boundary is
// Postgres' to enforce — this suite connects AS the context roles and
// asserts the boundary from the outside:
//
//   - game_app can read/write game.* and can execute schedule's two
//     SECURITY DEFINER round guards (the SchedulePort's in-tx gates) but
//     is DENIED every direct schedule.* statement, read or write;
//   - schedule_app can read/write schedule.* but is denied game.*;
//   - identity_app can read identity.* but is denied the other two.
//
// The suite owns its own throwaway database (the same harness contract as
// every consumer): LT_API_TEST_DSN unset → skip; set but unusable → hard
// failure. The DSN role needs LOGIN + CREATEDB + CREATEROLE (migration
// 000018 creates the context roles and needs to ALTER them).

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

var boundaryDB *DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		os.Exit(m.Run())
	}

	db, teardown, err := Setup(
		context.Background(),
		dsn,
		"boundary",
		"../../migrations/000001_create_initial_game_models.up.sql",
		"../../migrations/000002_create_users_table.up.sql",
		"../../migrations/000003_create_user_tokens_table.up.sql",
		"../../migrations/000004_add_permissions.up.sql",
		"../../migrations/000005_add_matches_ended_at.up.sql",
		"../../migrations/000006_create_rides_table.up.sql",
		"../../migrations/000007_alter_matches_odds_precision.up.sql",
		"../../migrations/000008_add_round_lifecycle_gates.up.sql",
		"../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
		"../../migrations/000010_add_idempotency_keys_table.up.sql",
		"../../migrations/000011_add_rides_result_agreement_gate.up.sql",
		"../../migrations/000012_add_rides_insert_state_gate.up.sql",
		"../../migrations/000013_add_rides_money_state_coupling.up.sql",
		"../../migrations/000014_relax_rounds_close_gate.up.sql",
		"../../migrations/000015_split_context_schemas.up.sql",
		"../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
		"../../migrations/000017_app_level_result_agreement.up.sql",
		"../../migrations/000018_context_roles.up.sql",
		"../../migrations/000019_add_status_rank_gates.up.sql",
		"../../migrations/000020_respect_supplied_match_ended_at.up.sql",
		"../../migrations/000021_add_game_delete_players_for_season.up.sql",
	)
	if err != nil {
		println("LT_API_TEST_DSN set but unusable, boundary test: " + err.Error())
		os.Exit(1)
	}
	boundaryDB = db

	code := m.Run()
	teardown()
	os.Exit(code)
}

// denied asserts the statement fails with SQLSTATE 42501 (insufficient
// privilege) — the boundary's enforcement code, not a shape error.
func denied(t *testing.T, pool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) {
	t.Helper()

	_, err := pool.Exec(context.Background(), sql, args...)
	if err == nil {
		t.Fatalf("statement crossed the boundary: %q — expected permission denied", sql)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("statement %q failed with %v, want SQLSTATE 42501", sql, err)
	}
}

// allowed asserts the statement runs (any non-privilege outcome is fine —
// a CHECK violation from an empty INSERT, say, still proved the
// privilege).
func allowed(t *testing.T, pool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) {
	t.Helper()

	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			t.Fatalf("statement %q was denied inside its own context: %v", sql, err)
		}
	}
}

func TestGameAppBoundary(t *testing.T) {
	if boundaryDB == nil {
		t.Skip("LT_API_TEST_DSN not set; boundary test skipped")
	}
	game := boundaryDB.Game

	// Own context: read and write both work (the 32-byte request_hash is
	// the table's own CHECK, no extension needed).
	allowed(t, game, `SELECT * FROM rides`)
	allowed(t, game, `SELECT * FROM players`)
	allowed(t, game, `SELECT * FROM idempotency_keys`)
	allowed(t, game, `INSERT INTO idempotency_keys (key, endpoint, request_hash, response_status, response_body)
		VALUES ('boundary-probe', 'boundary', decode(repeat('00', 32), 'hex'), 200, '\x00'::bytea)`)
	allowed(t, game, `DELETE FROM idempotency_keys WHERE key = 'boundary-probe'`)

	// The SchedulePort's definer guards: schedule's own operations, the
	// only schedule surface game may touch. NULL/false for unknown ids is
	// the facts-only answer.
	allowed(t, game, `SELECT schedule.round_open_guard($1)`, 999)
	allowed(t, game, `SELECT schedule.destination_open_guard($1)`, 999)

	// Every direct schedule statement is denied — reads and writes alike,
	// on every schedule table, even the two the port's facts come from.
	denied(t, game, `SELECT * FROM schedule.matches`)
	denied(t, game, `SELECT * FROM schedule.rounds`)
	denied(t, game, `SELECT * FROM schedule.seasons`)
	denied(t, game, `SELECT * FROM schedule.teams`)
	denied(t, game, `INSERT INTO schedule.teams (name, logo, description) VALUES ('x', 'y', 'z')`)
	denied(t, game, `UPDATE schedule.rounds SET status = 'closed'`)
	denied(t, game, `DELETE FROM schedule.matches`)
}

func TestScheduleAppBoundary(t *testing.T) {
	if boundaryDB == nil {
		t.Skip("LT_API_TEST_DSN not set; boundary test skipped")
	}
	sched := boundaryDB.Schedule

	allowed(t, sched, `SELECT * FROM seasons`)
	allowed(t, sched, `SELECT * FROM teams`)
	allowed(t, sched, `SELECT * FROM rounds`)
	allowed(t, sched, `SELECT * FROM matches`)

	denied(t, sched, `SELECT * FROM game.rides`)
	denied(t, sched, `SELECT * FROM game.players`)
	denied(t, sched, `SELECT * FROM game.idempotency_keys`)
	denied(t, sched, `INSERT INTO game.idempotency_keys (key, endpoint, request_hash, response_status, response_body)
		VALUES ('x', 'x', decode(repeat('00', 32), 'hex'), 200, '\x00'::bytea)`)
	denied(t, sched, `UPDATE game.rides SET state = 'lost'`)
}

func TestIdentityAppBoundary(t *testing.T) {
	if boundaryDB == nil {
		t.Skip("LT_API_TEST_DSN not set; boundary test skipped")
	}
	idp := boundaryDB.Identity

	allowed(t, idp, `SELECT * FROM users`)
	allowed(t, idp, `SELECT * FROM user_tokens`)
	allowed(t, idp, `SELECT * FROM roles`)
	allowed(t, idp, `SELECT * FROM permissions`)
	allowed(t, idp, `SELECT * FROM roles_permissions`)

	denied(t, idp, `SELECT * FROM game.players`)
	denied(t, idp, `SELECT * FROM schedule.teams`)
}

// TestIdentityAppInsertAllowed pins that identity_app may actually write
// its own tables (the INSERT probe above shares the statement for
// readability; the real assertion lives here).
func TestIdentityAppInsertAllowed(t *testing.T) {
	if boundaryDB == nil {
		t.Skip("LT_API_TEST_DSN not set; boundary test skipped")
	}

	// role_id has no default: pick the seeded 'user' role, then clean up.
	allowed(t, boundaryDB.Identity, `
		INSERT INTO users (name, email, password_hash, activated, role_id)
		VALUES ('boundary', 'boundary@example.com', 'h', true, (SELECT id FROM roles WHERE name = 'user'))`)
	allowed(t, boundaryDB.Identity, `DELETE FROM users WHERE email = 'boundary@example.com'`)
}
