// Package testdb sets up a throwaway PostgreSQL database for integration
// tests.
//
// Conventions shared by every consumer (internal/shared/server E2E,
// internal/game):
//
//   - The DSN comes from LT_API_TEST_DSN and points at the test database,
//     which does NOT need to exist. The role in the DSN only needs LOGIN +
//     CREATEDB and CREATEROLE (migration 000018 creates the per-context
//     roles): the harness creates the database itself (and therefore owns
//     it, and therefore may drop it). Nothing else is
//     granted.
//   - CREATE/DROP DATABASE must run outside a transaction, so they go through
//     a separate connection to the "postgres" maintenance database on the
//     same server, using the same credentials.
//   - Each test package gets its own database named <dsn-db>_<suite>, so
//     `go test ./...` (which runs packages in parallel) never has two
//     binaries fighting over one database.
//   - The database is dropped and recreated on every run: migrations are part
//     of what integration tests exercise, so the schema is always built from
//     the migration files, never inherited from a previous run.
//   - The harness hands out FOUR pools: Owner (the DSN role —
//     fixture planting, direct constraint probes, the api composition root's
//     reset) and one per context role (schedule_app / game_app /
//     identity_app, via SET ROLE on every connection — see ContextPool), so
//     the store tests exercise the real privilege regime.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB carries the pools a test suite runs against: Owner for fixtures and
// direct probes, one restricted pool per context role for the stores.
type DB struct {
	Owner    *pgxpool.Pool
	Schedule *pgxpool.Pool
	Game     *pgxpool.Pool
	Identity *pgxpool.Pool
}

// Context role names — the same the server wiring uses; they exist only
// after migration 000018, which every suite applies.
const (
	RoleSchedule = "schedule_app"
	RoleGame     = "game_app"
	RoleIdentity = "identity_app"
)

// Setup drops and recreates the test database for the given suite, applies
// each migration file in order, and returns the pools plus a teardown
// function that closes them and drops the database.
//
// dsn points at the test database (the name is used as a prefix; the actual
// database is <name>_<suite>). suite must be a short identifier unique to the
// calling test package, e.g. "api" or "game".
func Setup(ctx context.Context, dsn, suite string, migrationFiles ...string) (*DB, func(), error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse test DSN: %w", err)
	}
	base := strings.TrimPrefix(u.Path, "/")
	if base == "" {
		return nil, nil, fmt.Errorf("test DSN has no database name: %q", dsn)
	}
	dbName := base + "_" + suite

	admin, err := connect(ctx, dsn, "postgres", false)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to maintenance database: %w", describeConnectError(err))
	}

	// Identifiers are sanitized, so even odd database names are safe here.
	drop := fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{dbName}.Sanitize())
	create := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize())
	if _, err := admin.Exec(ctx, drop); err != nil {
		_ = admin.Close(ctx)
		return nil, nil, fmt.Errorf("drop test database %s: %w", dbName, err)
	}
	if _, err := admin.Exec(ctx, create); err != nil {
		_ = admin.Close(ctx)
		return nil, nil, fmt.Errorf("create test database %s: %w", dbName, err)
	}
	_ = admin.Close(ctx)

	// Migration files contain multiple statements, which the default
	// (extended) protocol cannot run in one Exec. Apply them over a dedicated
	// simple-protocol connection, then open the pool with default settings.
	for _, file := range migrationFiles {
		sqlBytes, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, fmt.Errorf("read migration %s: %w", file, err)
		}
		conn, err := connect(ctx, dsn, dbName, true)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to %s: %w", dbName, err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			_ = conn.Close(ctx)
			return nil, nil, fmt.Errorf("apply migration %s: %w", file, err)
		}
		_ = conn.Close(ctx)
	}

	owner, err := pgxpool.New(ctx, dsnWithName(dsn, dbName))
	if err != nil {
		return nil, nil, fmt.Errorf("connect owner pool to %s: %w", dbName, err)
	}

	schedulePool, err := ContextPool(ctx, dsn, dbName, RoleSchedule)
	if err != nil {
		owner.Close()
		return nil, nil, fmt.Errorf("connect schedule pool to %s: %w", dbName, err)
	}
	gamePool, err := ContextPool(ctx, dsn, dbName, RoleGame)
	if err != nil {
		owner.Close()
		schedulePool.Close()
		return nil, nil, fmt.Errorf("connect game pool to %s: %w", dbName, err)
	}
	identityPool, err := ContextPool(ctx, dsn, dbName, RoleIdentity)
	if err != nil {
		owner.Close()
		schedulePool.Close()
		gamePool.Close()
		return nil, nil, fmt.Errorf("connect identity pool to %s: %w", dbName, err)
	}

	teardown := func() {
		owner.Close()
		schedulePool.Close()
		gamePool.Close()
		identityPool.Close()
		admin, err := connect(context.Background(), dsn, "postgres", false)
		if err != nil {
			return // pools already closed; nothing else to clean up
		}
		_, _ = admin.Exec(context.Background(), drop)
		_ = admin.Close(context.Background())
	}

	return &DB{
		Owner:    owner,
		Schedule: schedulePool,
		Game:     gamePool,
		Identity: identityPool,
	}, teardown, nil
}

// ContextPool opens a pool whose every connection runs SET ROLE <role> —
// the same post-connect hook the server wiring uses (migration 000018
// created the roles and granted them exactly their own schema). Store
// tests run against these pools so the privilege regime the boundary
// rests on is exercised, not just the SQL. dbName is the database Setup
// created (the DSN's name + "_" + suite).
func ContextPool(ctx context.Context, dsn, dbName, role string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsnWithName(dsn, dbName))
	if err != nil {
		return nil, err
	}
	// SET ROLE + SET search_path pair exactly like the server's
	// post-connect hook: role-level ALTER ROLE settings apply only at
	// session start, never on SET ROLE, so the hook carries both.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return fmt.Errorf("SET ROLE %s: %w", role, err)
		}
		schema := strings.TrimSuffix(role, "_app")
		if _, err := conn.Exec(ctx, "SET search_path = "+schema+", public"); err != nil {
			return fmt.Errorf("SET search_path %s: %w", schema, err)
		}
		return nil
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// describeConnectError turns a failed connection to the maintenance database
// into an actionable message: the DSN set but wrong (bad credentials, missing
// role, unreachable server) is the most common setup mistake, and a raw pgx
// dial dump does not say what to do about it.
func describeConnectError(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "28P01": // invalid_password
			return fmt.Errorf("authentication failed for the role in LT_API_TEST_DSN: %w", err)
		case "28000": // invalid_authorization_specification (e.g. role does not exist)
			return fmt.Errorf("the role in LT_API_TEST_DSN is invalid or does not exist: %w", err)
		case "3D000": // invalid_catalog_name
			return fmt.Errorf("the maintenance database \"postgres\" does not exist on the server in LT_API_TEST_DSN: %w", err)
		case "42501": // insufficient_privilege
			return fmt.Errorf("the role in LT_API_TEST_DSN lacks privileges (it needs LOGIN + CREATEDB): %w", err)
		}
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return fmt.Errorf("cannot reach the PostgreSQL server in LT_API_TEST_DSN (is it running, and is host:port reachable from here?): %w", err)
	}
	return err
}

// connect opens a single connection to targetDB on the server described by
// dsn. simpleProtocol is required to execute multi-statement SQL files.
func connect(ctx context.Context, dsn, targetDB string, simpleProtocol bool) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsnWithName(dsn, targetDB))
	if err != nil {
		return nil, err
	}
	if simpleProtocol {
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// dsnWithURLPath returns dsn with the database name replaced. It re-parses so
// the password never needs escaping by hand in callers.
func dsnWithName(dsn, dbName string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn // unreachable: Setup already parsed this DSN successfully
	}
	u.Path = "/" + dbName
	return u.String()
}
