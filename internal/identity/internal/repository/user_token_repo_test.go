package repository

// Tests for UserTokenRepository and UserRepository.GetForToken — the query
// the authenticate middleware resolves every Bearer token with.
//
// Integration tests against the "identity_repository" suite database
// (migrations 000002–000004, see TestMain); the generator itself is
// unit-tested in the model package.

import (
	"context"
	"errors"
	"testing"
	"time"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

func TestNewAndGetForToken(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)
	tokens := NewUserTokenRepository(identityPool)

	seedUser := func(t *testing.T) model.User {
		t.Helper()
		resetUsers(t)

		inserted, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
		return inserted
	}

	t.Run("New persists a resolvable token", func(t *testing.T) {
		inserted := seedUser(t)

		token, err := tokens.New(context.Background(), inserted.ID, time.Hour, model.ScopeAuthentication)
		if err != nil {
			t.Fatalf("UserTokens.New: %v", err)
		}

		got, err := users.GetForToken(context.Background(), model.ScopeAuthentication, token.Plaintext)
		if err != nil {
			t.Fatalf("GetForToken: %v", err)
		}
		if got.ID != inserted.ID {
			t.Errorf("GetForToken user id = %d, want %d", got.ID, inserted.ID)
		}
	})

	t.Run("wrong scope does not resolve the token", func(t *testing.T) {
		inserted := seedUser(t)

		// An activation token must never authenticate: scope is part of the
		// lookup, so one table serves several token kinds without leaking.
		token, err := tokens.New(context.Background(), inserted.ID, time.Hour, model.ScopeActivation)
		if err != nil {
			t.Fatalf("UserTokens.New: %v", err)
		}

		_, err = users.GetForToken(context.Background(), model.ScopeAuthentication, token.Plaintext)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("GetForToken with wrong scope: got %v, want ErrNotFound", err)
		}
	})

	t.Run("expired token maps to ErrNotFound", func(t *testing.T) {
		inserted := seedUser(t)

		// Negative ttl: the row is born expired. The expiry check lives in
		// the WHERE clause, so the expired row resolves as "not found" —
		// exactly what an invalid-token 401 needs.
		token, err := tokens.New(context.Background(), inserted.ID, -time.Minute, model.ScopeAuthentication)
		if err != nil {
			t.Fatalf("UserTokens.New with negative ttl: %v", err)
		}

		_, err = users.GetForToken(context.Background(), model.ScopeAuthentication, token.Plaintext)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("GetForToken for expired token: got %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown plaintext maps to ErrNotFound", func(t *testing.T) {
		seedUser(t)

		_, err := users.GetForToken(context.Background(), model.ScopeAuthentication, "zzzzzzzzzzzzzzzzzzzzzzzzzz")
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("GetForToken for unknown token: got %v, want ErrNotFound", err)
		}
	})
}

func TestDeleteAllForUser(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)
	tokens := NewUserTokenRepository(identityPool)

	t.Run("deletes only the target scope of the target user", func(t *testing.T) {
		resetUsers(t)

		seed := func(t *testing.T, name, email string) model.User {
			t.Helper()
			user, err := users.Insert(context.Background(), mustUser(name, email, "pa55word123"))
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
			{alice.ID, model.ScopeAuthentication},
			{alice.ID, model.ScopeActivation},
			{bob.ID, model.ScopeAuthentication},
		} {
			if _, err := tokens.New(context.Background(), spec.userID, time.Hour, spec.scope); err != nil {
				t.Fatalf("seed token: %v", err)
			}
		}

		if err := tokens.DeleteAllForUser(context.Background(), model.ScopeAuthentication, alice.ID); err != nil {
			t.Fatalf("DeleteAllForUser: %v", err)
		}

		var count int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM identity.user_tokens WHERE user_id = $1 AND scope = $2`,
			alice.ID, model.ScopeAuthentication,
		).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("%d authentication tokens left for alice, want 0", count)
		}

		// Alice's activation token and Bob's auth token must survive.
		if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM identity.user_tokens`).Scan(&count); err != nil {
			t.Fatalf("count all: %v", err)
		}
		if count != 2 {
			t.Errorf("%d tokens left overall, want 2 (activation + other user)", count)
		}
	})
}
