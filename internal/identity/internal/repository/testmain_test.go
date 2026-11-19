package repository

// Integration-test harness for the identity repositories: users, user_tokens
// and permissions run against a real PostgreSQL database (see
// internal/testdb for the setup contract).
//
//   - LT_API_TEST_DSN unset → DB-backed tests skip, so `go test ./...`
//     still passes on a DB-less machine.
//   - LT_API_TEST_DSN set but the database is unreachable → hard failure.
//     A green run that silently tested nothing is worse than a red one.
//
// Suite name "identity_repository" gives this package its own database, so
// `go test ./...` (parallel packages) never contends with the api, game,
// service or other context suites.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/testdb"
)

// migFiles lists the full migration series: 000015 moves whatever tables
// exist, and 000016–000018 (FK surgery, roles) assume the whole preceding
// chain — so identity tests run the same layout the server runs, including
// the calendar/game tables they do not touch.
var migFiles = []string{
	"../../../../migrations/000001_create_initial_game_models.up.sql",
	"../../../../migrations/000002_create_users_table.up.sql",
	"../../../../migrations/000003_create_user_tokens_table.up.sql",
	"../../../../migrations/000004_add_permissions.up.sql",
	"../../../../migrations/000005_add_matches_ended_at.up.sql",
	"../../../../migrations/000006_create_rides_table.up.sql",
	"../../../../migrations/000007_alter_matches_odds_precision.up.sql",
	"../../../../migrations/000008_add_round_lifecycle_gates.up.sql",
	"../../../../migrations/000009_add_rides_won_pending_partial_index.up.sql",
	"../../../../migrations/000010_add_idempotency_keys_table.up.sql",
	"../../../../migrations/000011_add_rides_result_agreement_gate.up.sql",
	"../../../../migrations/000012_add_rides_insert_state_gate.up.sql",
	"../../../../migrations/000013_add_rides_money_state_coupling.up.sql",
	"../../../../migrations/000014_relax_rounds_close_gate.up.sql",
	"../../../../migrations/000015_split_context_schemas.up.sql",
	"../../../../migrations/000016_drop_cross_context_foreign_keys.up.sql",
	"../../../../migrations/000017_app_level_result_agreement.up.sql",
	"../../../../migrations/000018_context_roles.up.sql",
	// 000019-000021 run the full production chain: the status-rank gates, the
	// ended_at stamp that keeps a supplied end time, and the game-side
	// definer delete.
	"../../../../migrations/000019_add_status_rank_gates.up.sql",
	"../../../../migrations/000020_respect_supplied_match_ended_at.up.sql",
	"../../../../migrations/000021_add_game_delete_players_for_season.up.sql",
}

// testPool runs as the owner: fixtures and direct assertions.
// identityPool runs as identity_app: the repositories under test exercise
// the real privilege regime.
var (
	testPool     *pgxpool.Pool
	identityPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	// Fixtures hash passwords through the model; no assertion here depends on
	// the hash being slow, so the suite pays the cheapest cost.
	model.PasswordCost = bcrypt.MinCost

	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		fmt.Println("LT_API_TEST_DSN not set; identity repository integration tests will be skipped")
		os.Exit(m.Run())
	}

	db, teardown, err := testdb.Setup(context.Background(), dsn, "identity_repository", migFiles...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test database setup: %v\n", err)
		os.Exit(1)
	}
	testPool = db.Owner
	identityPool = db.Identity

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

// resetUsers wipes the identity tables with identity restart, so ID-based
// assertions (first insert → id 1) hold in every test. roles/permissions are
// reference data from migration 000004 and stay untouched. The token table
// references users (FK), so user_tokens goes in the same TRUNCATE.
func resetUsers(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `TRUNCATE identity.users, identity.user_tokens RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
}

// mustUser returns a user with a valid hash already set — the state the
// service is responsible for producing before calling the repository.
func mustUser(name, email, plaintext string) model.User {
	user := model.User{Name: name, Email: email}
	if err := user.Password.Set(plaintext); err != nil {
		panic(fmt.Sprintf("test helper: hash %q: %v", plaintext, err))
	}
	return user
}
