package identity

// Tests for the users store and its validators (internal/identity/users.go).
//
// Two layers, mirroring the repo convention:
//
//   - Pure unit tests (no DB): ValidateRegistration's field rules and the
//     bcrypt hash round-trip. These run everywhere, DB or not.
//   - Integration tests against the real test database (see
//     internal/testdb): Insert/GetByEmail/Update, including the
//     duplicate-email translation (23505 on users_email_key →
//     ErrDuplicateEmail) and the optimistic-concurrency behavior of Update.
//
// Suite name "identity" gives this package its own database, so
// `go test ./...` never contends with the api/game suites.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	storepkg "lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/testdb"
	"lt-api.aleksrdvn.com/internal/validator"
)

// Integration-test harness, same contract as the api package (see
// internal/api/testmain_test.go): LT_API_TEST_DSN unset → tests skip;
// set but unreachable → hard failure, because a green run that silently
// tested nothing is worse than a red one.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("LT_API_TEST_DSN")
	if dsn == "" {
		fmt.Println("LT_API_TEST_DSN not set; identity integration tests will be skipped")
		os.Exit(m.Run())
	}

	pool, teardown, err := testdb.Setup(
		context.Background(),
		dsn,
		"identity",
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

// resetUsers wipes the identity tables with identity restart, so ID-based
// assertions (first insert → id 1) hold in every test. roles/permissions are
// reference data from migration 000004 and stay untouched. The token store
// references users (FK), so user_tokens goes in the same TRUNCATE.
func resetUsers(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `TRUNCATE users, user_tokens RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
}

// mustUser returns a user with a valid hash already set — the state the
// handler is responsible for producing before calling the store.
func mustUser(name, email, plaintext string) User {
	user := User{Name: name, Email: email}
	if err := user.Password.Set(plaintext); err != nil {
		panic(fmt.Sprintf("test helper: hash %q: %v", plaintext, err))
	}
	return user
}

// --- pure unit tests (no database) ---

// TestValidateRegistration pins the field rules of the only user validator.
// The table shape mirrors internal/game's validator tests: expected errors
// as a field → message map, empty map = must be valid.
func TestValidateRegistration(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		email    string
		password string
		wantErrs map[string]string
	}{
		{
			name:     "valid registration",
			userName: "Alice Example",
			email:    "alice@example.com",
			password: "pa55word123",
			wantErrs: map[string]string{},
		},
		{
			name:     "empty name",
			email:    "a@example.com",
			password: "pa55word123",
			wantErrs: map[string]string{"name": "must be provided"},
		},
		{
			name:     "name over 500 bytes",
			userName: strings.Repeat("n", 501),
			email:    "a@example.com",
			password: "pa55word123",
			wantErrs: map[string]string{"name": "must not be more then 500 bytes long"},
		},
		{
			name:     "name at the 500-byte limit is valid",
			userName: strings.Repeat("n", 500),
			email:    "a@example.com",
			password: "pa55word123",
			wantErrs: map[string]string{},
		},
		{
			name:     "empty email",
			userName: "Alice",
			password: "pa55word123",
			wantErrs: map[string]string{"email": "must be provided"},
		},
		{
			name:     "invalid email format",
			userName: "Alice",
			email:    "not-an-email",
			password: "pa55word123",
			wantErrs: map[string]string{"email": "must be a valid email address"},
		},
		{
			name:     "empty password",
			userName: "Alice",
			email:    "a@example.com",
			wantErrs: map[string]string{"password": "must be provided"},
		},
		{
			name:     "password too short",
			userName: "Alice",
			email:    "a@example.com",
			password: "short",
			wantErrs: map[string]string{"password": "must be at least 8 bytes long"},
		},
		{
			// bcrypt refuses input over 72 bytes; validating the plaintext
			// first is what turns that into a 422 instead of a 500 (see the
			// comment on ValidateRegistration).
			name:     "password over 72 bytes",
			userName: "Alice",
			email:    "a@example.com",
			password: strings.Repeat("p", 73),
			wantErrs: map[string]string{"password": "must not be more then 72 bytes long"},
		},
		{
			name:     "password at the 72-byte limit is valid",
			userName: "Alice",
			email:    "a@example.com",
			password: strings.Repeat("p", 72),
			wantErrs: map[string]string{},
		},
		{
			// Validators keep collecting: one bad field must not hide the
			// errors of the others.
			name:     "all fields invalid reports every error",
			userName: "",
			email:    "nope",
			password: "x",
			wantErrs: map[string]string{
				"name":     "must be provided",
				"email":    "must be a valid email address",
				"password": "must be at least 8 bytes long",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validator.New()
			ValidateRegistration(v, tt.userName, tt.email, tt.password)

			if len(v.Errors) != len(tt.wantErrs) {
				t.Fatalf("got errors %v, want %v", v.Errors, tt.wantErrs)
			}
			for field, wantMsg := range tt.wantErrs {
				gotMsg, ok := v.Errors[field]
				if !ok {
					t.Errorf("expected error on field %q, got errors %v", field, v.Errors)
				} else if gotMsg != wantMsg {
					t.Errorf("field %q: got message %q, want %q", field, gotMsg, wantMsg)
				}
			}
		})
	}
}

// TestPasswordHashRoundTrip covers Set/Matches: the plaintext is never the
// stored value, a correct password matches, and a wrong password is a normal
// negative result (false, nil) — that distinction is what lets handlers
// answer 401 without special-casing bcrypt's sentinel error.
func TestPasswordHashRoundTrip(t *testing.T) {
	var p password
	if err := p.Set("pa55word123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.plaintext == nil || *p.plaintext != "pa55word123" {
		t.Error("Set must remember the plaintext")
	}
	if string(p.hash) == "pa55word123" {
		t.Fatal("hash must not store the plaintext")
	}

	ok, err := p.Matches("pa55word123")
	if err != nil || !ok {
		t.Errorf("Matches(correct password) = %v, %v; want true, nil", ok, err)
	}

	ok, err = p.Matches("wrong-password")
	if err != nil || ok {
		t.Errorf("Matches(wrong password) = %v, %v; want false, nil", ok, err)
	}

	// Matching against a zero-value password (no hash set) is a genuine
	// error state, not a silent "no match".
	var empty password
	ok, err = empty.Matches("pa55word123")
	if ok {
		t.Error("Matches against unset hash must not succeed")
	}
	if err == nil {
		t.Error("Matches against unset hash must return an error")
	}
}

// --- integration tests (real database) ---

func TestInsert(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	t.Run("returns the persisted user with server-generated fields", func(t *testing.T) {
		resetUsers(t)

		got, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}

		if got.ID != 1 {
			t.Errorf("ID = %d, want 1 (first row after identity restart)", got.ID)
		}
		if got.Version != 1 {
			t.Errorf("Version = %d, want 1", got.Version)
		}
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt must be populated by the database")
		}
		if got.Activated {
			t.Error("Activated must round-trip as set by the caller (false here)")
		}
	})

	t.Run("hashes are stored, plaintext is not", func(t *testing.T) {
		resetUsers(t)

		want := mustUser("Alice", "alice@example.com", "pa55word123")
		_, err := store.Users.Insert(context.Background(), want)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}

		var storedHash []byte
		err = testPool.QueryRow(context.Background(),
			`SELECT password_hash FROM users WHERE email = 'alice@example.com'`).Scan(&storedHash)
		if err != nil {
			t.Fatalf("row not found after insert: %v", err)
		}
		if string(storedHash) == "pa55word123" {
			t.Fatal("plaintext password stored in password_hash")
		}
		ok, err := (&password{hash: storedHash}).Matches("pa55word123")
		if err != nil || !ok {
			t.Errorf("stored hash does not match the inserted password: %v, %v", ok, err)
		}
	})

	t.Run("duplicate email maps to ErrDuplicateEmail", func(t *testing.T) {
		resetUsers(t)

		_, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// Different case, same email: citext compares case-insensitively, so
		// this must hit the unique constraint, not create a second row.
		_, err = store.Users.Insert(context.Background(), mustUser("Alice Again", "ALICE@EXAMPLE.COM", "pa55word123"))
		if !errors.Is(err, ErrDuplicateEmail) {
			t.Fatalf("Insert with duplicate email: got %v, want ErrDuplicateEmail", err)
		}
	})

	t.Run("panics on missing password hash", func(t *testing.T) {
		resetUsers(t)

		// Invariant, not validation: the store refuses to persist a user
		// without a hash loudly (panic) rather than relying on the DB's NOT
		// NULL as the only line of defense.
		defer func() {
			if recover() == nil {
				t.Error("Insert without a password hash must panic")
			}
		}()
		_, _ = store.Users.Insert(context.Background(), User{Name: "NoHash", Email: "nohash@example.com"})
	})
}

func TestGetByEmail(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	t.Run("round-trips a stored user", func(t *testing.T) {
		resetUsers(t)

		inserted, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		got, err := store.Users.GetByEmail(context.Background(), "alice@example.com")
		if err != nil {
			t.Fatalf("GetByEmail: %v", err)
		}
		if got.ID != inserted.ID || got.Name != "Alice" || got.Version != inserted.Version {
			t.Errorf("GetByEmail = %+v, want the inserted user %+v", got, inserted)
		}
		ok, err := got.Password.Matches("pa55word123")
		if err != nil || !ok {
			t.Errorf("retrieved hash does not match the stored password: %v, %v", ok, err)
		}
	})

	t.Run("lookup is case-insensitive via citext", func(t *testing.T) {
		resetUsers(t)

		_, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		got, err := store.Users.GetByEmail(context.Background(), "ALICE@EXAMPLE.COM")
		if err != nil {
			t.Fatalf("GetByEmail with uppercase email: %v", err)
		}
		if got.Name != "Alice" {
			t.Errorf("case-insensitive lookup returned user %q, want Alice", got.Name)
		}
	})

	t.Run("unknown email maps to ErrRecordNotFound", func(t *testing.T) {
		resetUsers(t)

		_, err := store.Users.GetByEmail(context.Background(), "ghost@example.com")
		if !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Fatalf("GetByEmail for unknown email: got %v, want store.ErrRecordNotFound", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	seed := func(t *testing.T) User {
		t.Helper()
		resetUsers(t)
		user, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
		return user
	}

	t.Run("persists changes and bumps the version", func(t *testing.T) {
		user := seed(t)

		user.Name = "Alice Updated"
		user.Activated = true
		if err := user.Password.Set("new-password-9"); err != nil {
			t.Fatalf("Set: %v", err)
		}

		got, err := store.Users.Update(context.Background(), user)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got.Version != user.Version+1 {
			t.Errorf("Version after update = %d, want %d", got.Version, user.Version+1)
		}

		reread, err := store.Users.GetByEmail(context.Background(), "alice@example.com")
		if err != nil {
			t.Fatalf("GetByEmail after update: %v", err)
		}
		if reread.Name != "Alice Updated" {
			t.Errorf("name after update = %q, want %q", reread.Name, "Alice Updated")
		}
		if !reread.Activated {
			t.Error("activated flag did not persist")
		}
		ok, err := reread.Password.Matches("new-password-9")
		if err != nil || !ok {
			t.Errorf("updated hash does not match the new password: %v, %v", ok, err)
		}
	})

	t.Run("stale version is rejected", func(t *testing.T) {
		user := seed(t)

		// Simulate a concurrent writer: bump the row behind the caller's back.
		if err := user.Password.Set("concurrent-edit"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if _, err := store.Users.Update(context.Background(), user); err != nil {
			t.Fatalf("concurrent update: %v", err)
		}

		// Replay the caller's update with the now-stale version: the WHERE
		// clause matches no row, and the store translates that into
		// store.ErrEditConflict — the sentinel the API layer turns into a 409.
		user.Version = 1 // stale
		user.Name = "Loser Of The Race"
		_, err := store.Users.Update(context.Background(), user)
		if !errors.Is(err, storepkg.ErrEditConflict) {
			t.Errorf("Update with stale version: got %v, want store.ErrEditConflict", err)
		}
	})

	t.Run("duplicate email on update maps to ErrDuplicateEmail", func(t *testing.T) {
		user := seed(t)
		_, err := store.Users.Insert(context.Background(), mustUser("Bob", "bob@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// citext again: case differs, uniqueness still bites.
		user.Email = "BOB@EXAMPLE.COM"
		_, err = store.Users.Update(context.Background(), user)
		if !errors.Is(err, ErrDuplicateEmail) {
			t.Fatalf("Update to a duplicate email: got %v, want ErrDuplicateEmail", err)
		}
	})

	t.Run("panics on missing password hash", func(t *testing.T) {
		user := seed(t)
		user.Password.hash = nil

		defer func() {
			if recover() == nil {
				t.Error("Update without a password hash must panic")
			}
		}()
		_, _ = store.Users.Update(context.Background(), user)
	})
}
