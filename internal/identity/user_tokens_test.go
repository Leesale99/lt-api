package identity

// Tests for the user-token store and Users.GetForToken — the query the
// authenticate middleware resolves every Bearer token with.
//
// Layered like users_test.go: pure unit tests for the generator (no DB),
// integration tests for New/GetForToken/DeleteAllForUser against the real
// database (migrations 000002–000004, see TestMain there).

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	storepkg "lt-api.aleksrdvn.com/internal/store"
)

// --- pure unit tests (no database) ---

func TestGenerateUserToken(t *testing.T) {
	t.Run("plaintext is exactly 26 characters", func(t *testing.T) {
		token := generateUserToken(1, time.Hour, ScopeAuthentication)
		if len(token.Plaintext) != plaintextTokenLength {
			t.Errorf("plaintext length = %d, want %d", len(token.Plaintext), plaintextTokenLength)
		}
	})

	t.Run("hash is the SHA-256 of the plaintext", func(t *testing.T) {
		token := generateUserToken(1, time.Hour, ScopeAuthentication)
		want := sha256.Sum256([]byte(token.Plaintext))
		if !slices.Equal(token.Hash, want[:]) {
			t.Error("token.Hash does not equal SHA-256(plaintext)")
		}
	})

	t.Run("expiry honours the ttl", func(t *testing.T) {
		ttl := 2 * time.Hour
		token := generateUserToken(1, ttl, ScopeAuthentication)
		// Clock can tick between generation and assertion; allow a minute.
		if !token.Expiry.After(time.Now().Add(ttl - time.Minute)) {
			t.Errorf("expiry = %v, want ~now + %v", token.Expiry, ttl)
		}
	})

	t.Run("scope and user are carried", func(t *testing.T) {
		token := generateUserToken(42, time.Hour, ScopeActivation)
		if token.UserID != 42 || token.Scope != ScopeActivation {
			t.Errorf("token = (user %d, scope %q), want (42, %q)", token.UserID, token.Scope, ScopeActivation)
		}
	})

	t.Run("two generations never collide", func(t *testing.T) {
		first := generateUserToken(1, time.Hour, ScopeAuthentication)
		second := generateUserToken(1, time.Hour, ScopeAuthentication)
		if first.Plaintext == second.Plaintext {
			t.Error("two tokens share a plaintext: crypto/rand reuse")
		}
	})
}

// --- integration tests (real database) ---

func TestNewAndGetForToken(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	t.Run("New persists a resolvable token", func(t *testing.T) {
		resetUsers(t)

		inserted, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		token, err := store.UserTokens.New(context.Background(), inserted.ID, time.Hour, ScopeAuthentication)
		if err != nil {
			t.Fatalf("UserTokens.New: %v", err)
		}

		got, err := store.Users.GetForToken(context.Background(), ScopeAuthentication, token.Plaintext)
		if err != nil {
			t.Fatalf("GetForToken: %v", err)
		}
		if got.ID != inserted.ID {
			t.Errorf("GetForToken user id = %d, want %d", got.ID, inserted.ID)
		}
	})

	t.Run("wrong scope does not resolve the token", func(t *testing.T) {
		resetUsers(t)

		inserted, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// An activation token must never authenticate: scope is part of the
		// lookup, so one table serves several token kinds without leaking.
		token, err := store.UserTokens.New(context.Background(), inserted.ID, time.Hour, ScopeActivation)
		if err != nil {
			t.Fatalf("UserTokens.New: %v", err)
		}

		_, err = store.Users.GetForToken(context.Background(), ScopeAuthentication, token.Plaintext)
		if !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Errorf("GetForToken with wrong scope: got %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("expired token maps to ErrRecordNotFound", func(t *testing.T) {
		resetUsers(t)

		inserted, err := store.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// Negative ttl: the row is born expired. The expiry check lives in
		// the WHERE clause, so the expired row resolves as "not found" —
		// exactly what an invalid-token 401 needs.
		token, err := store.UserTokens.New(context.Background(), inserted.ID, -time.Minute, ScopeAuthentication)
		if err != nil {
			t.Fatalf("UserTokens.New with negative ttl: %v", err)
		}

		_, err = store.Users.GetForToken(context.Background(), ScopeAuthentication, token.Plaintext)
		if !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Errorf("GetForToken for expired token: got %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("unknown plaintext maps to ErrRecordNotFound", func(t *testing.T) {
		resetUsers(t)

		_, err := store.Users.GetForToken(context.Background(), ScopeAuthentication, "zzzzzzzzzzzzzzzzzzzzzzzzzz")
		if !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Errorf("GetForToken for unknown token: got %v, want ErrRecordNotFound", err)
		}
	})
}

func TestDeleteAllForUser(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	t.Run("deletes only the target scope of the target user", func(t *testing.T) {
		resetUsers(t)

		seed := func(t *testing.T, name, email string) User {
			t.Helper()
			user, err := store.Users.Insert(context.Background(), mustUser(name, email, "pa55word123"))
			if err != nil {
				t.Fatalf("seed insert: %v", err)
			}
			return user
		}

		alice := seed(t, "Alice", "alice@example.com")
		bob := seed(t, "Bob", "bob@example.com")

		for _, spec := range []struct {
			userID int
			scope  string
		}{
			{alice.ID, ScopeAuthentication},
			{alice.ID, ScopeActivation},
			{bob.ID, ScopeAuthentication},
		} {
			if _, err := store.UserTokens.New(context.Background(), spec.userID, time.Hour, spec.scope); err != nil {
				t.Fatalf("seed token: %v", err)
			}
		}

		if err := store.UserTokens.DeleteAllForUser(context.Background(), ScopeAuthentication, alice.ID); err != nil {
			t.Fatalf("DeleteAllForUser: %v", err)
		}

		var count int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM user_tokens WHERE user_id = $1 AND scope = $2`,
			alice.ID, ScopeAuthentication,
		).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("%d authentication tokens left for alice, want 0", count)
		}

		// Alice's activation token and Bob's auth token must survive.
		if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM user_tokens`).Scan(&count); err != nil {
			t.Fatalf("count all: %v", err)
		}
		if count != 2 {
			t.Errorf("%d tokens left overall, want 2 (activation + other user)", count)
		}
	})
}
