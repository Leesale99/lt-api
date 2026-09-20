package api

// Integration-test harness: every handler test in this package runs against
// a real PostgreSQL database (see internal/testdb for the setup contract).
//
//   - LT_API_TEST_DSN unset → every test skips, so `go test ./...` still
//     passes on a DB-less machine.
//   - LT_API_TEST_DSN set but the database is unreachable → hard failure.
//     A green run that silently tested nothing is worse than a red one.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		fmt.Println("LT_API_TEST_DSN not set; api integration tests will be skipped")
		os.Exit(m.Run())
	}

	pool, teardown, err := testdb.Setup(
		context.Background(),
		dsn,
		"api",
		"../../migrations/000001_create_initial_game_models.up.sql",
		"../../migrations/000002_create_users_table.up.sql",
		"../../migrations/000003_create_user_tokens_table.up.sql",
		"../../migrations/000004_add_permissions.up.sql",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()
	teardown()
	os.Exit(code)
}

// requireDB skips a test when the database harness is unavailable.
func requireDB(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("LT_API_TEST_DSN not set; requires a test database")
	}
}
