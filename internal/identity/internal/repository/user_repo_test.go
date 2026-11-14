package repository

// Tests for UserRepository against the real test database: Insert, Get,
// GetByEmail and Update, including the duplicate-email translation (23505 on
// users_email_key → model.ErrDuplicateEmail) and the optimistic-concurrency
// behaviour of Update.
//
// The validators and the password round-trip are pure tests in the model
// package; the activation transaction is tested in the service package.

import (
	"context"
	"errors"
	"testing"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

func TestInsert(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)

	t.Run("returns the persisted user with server-generated fields", func(t *testing.T) {
		resetUsers(t)

		got, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
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
		_, err := users.Insert(context.Background(), want)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}

		var storedHash []byte
		err = testPool.QueryRow(context.Background(),
			`SELECT password_hash FROM identity.users WHERE email = 'alice@example.com'`).Scan(&storedHash)
		if err != nil {
			t.Fatalf("row not found after insert: %v", err)
		}
		if string(storedHash) == "pa55word123" {
			t.Fatal("plaintext password stored in password_hash")
		}

		// Read it back through the repository and let the model compare: the
		// hash that reached storage is the one the entity matches against.
		reread, err := users.GetByEmail(context.Background(), "alice@example.com")
		if err != nil {
			t.Fatalf("GetByEmail after insert: %v", err)
		}
		ok, err := reread.Password.Matches("pa55word123")
		if err != nil || !ok {
			t.Errorf("stored hash does not match the inserted password: %v, %v", ok, err)
		}
	})

	t.Run("duplicate email maps to ErrDuplicateEmail", func(t *testing.T) {
		resetUsers(t)

		_, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// Different case, same email: citext compares case-insensitively, so
		// this must hit the unique constraint, not create a second row.
		_, err = users.Insert(context.Background(), mustUser("Alice Again", "ALICE@EXAMPLE.COM", "pa55word123"))
		if !errors.Is(err, model.ErrDuplicateEmail) {
			t.Fatalf("Insert with duplicate email: got %v, want ErrDuplicateEmail", err)
		}
	})

	t.Run("panics on missing password hash", func(t *testing.T) {
		resetUsers(t)

		// Invariant, not validation: the repository refuses to persist a user
		// without a hash loudly (panic) rather than relying on the DB's NOT
		// NULL as the only line of defense.
		defer func() {
			if recover() == nil {
				t.Error("Insert without a password hash must panic")
			}
		}()
		_, _ = users.Insert(context.Background(), model.User{Name: "NoHash", Email: "nohash@example.com"})
	})
}

func TestGetByEmail(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)

	t.Run("round-trips a stored user", func(t *testing.T) {
		resetUsers(t)

		inserted, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		got, err := users.GetByEmail(context.Background(), "alice@example.com")
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

		_, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		got, err := users.GetByEmail(context.Background(), "ALICE@EXAMPLE.COM")
		if err != nil {
			t.Fatalf("GetByEmail with uppercase email: %v", err)
		}
		if got.Name != "Alice" {
			t.Errorf("case-insensitive lookup returned user %q, want Alice", got.Name)
		}
	})

	t.Run("unknown email maps to ErrNotFound", func(t *testing.T) {
		resetUsers(t)

		_, err := users.GetByEmail(context.Background(), "ghost@example.com")
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("GetByEmail for unknown email: got %v, want model.ErrNotFound", err)
		}
	})
}

func TestGet(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)

	t.Run("returns the stored user by id", func(t *testing.T) {
		resetUsers(t)

		inserted, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		got, err := users.Get(context.Background(), inserted.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != inserted.ID || got.Email != "alice@example.com" {
			t.Errorf("Get = %+v, want the inserted user %+v", got, inserted)
		}
	})

	t.Run("unknown id maps to ErrNotFound", func(t *testing.T) {
		resetUsers(t)

		_, err := users.Get(context.Background(), 999)
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("Get for unknown id: got %v, want model.ErrNotFound", err)
		}
	})

	t.Run("non-positive id maps to ErrNotFound without a query", func(t *testing.T) {
		for _, id := range []int{0, -1} {
			_, err := users.Get(context.Background(), id)
			if !errors.Is(err, model.ErrNotFound) {
				t.Errorf("Get(%d): got %v, want model.ErrNotFound", id, err)
			}
		}
	})
}

func TestUpdate(t *testing.T) {
	requireDB(t)
	users := NewUserRepository(identityPool)

	seed := func(t *testing.T) model.User {
		t.Helper()
		resetUsers(t)
		user, err := users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
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

		got, err := users.Update(context.Background(), user)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got.Version != user.Version+1 {
			t.Errorf("Version after update = %d, want %d", got.Version, user.Version+1)
		}

		reread, err := users.GetByEmail(context.Background(), "alice@example.com")
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
		if _, err := users.Update(context.Background(), user); err != nil {
			t.Fatalf("concurrent update: %v", err)
		}

		// Replay the caller's update with the now-stale version: the WHERE
		// clause matches no row, and the repository translates that into
		// model.ErrEditConflict — the sentinel the API layer turns into a 409.
		user.Version = 1 // stale
		user.Name = "Loser Of The Race"
		_, err := users.Update(context.Background(), user)
		if !errors.Is(err, model.ErrEditConflict) {
			t.Errorf("Update with stale version: got %v, want model.ErrEditConflict", err)
		}
	})

	t.Run("duplicate email on update maps to ErrDuplicateEmail", func(t *testing.T) {
		user := seed(t)
		_, err := users.Insert(context.Background(), mustUser("Bob", "bob@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}

		// citext again: case differs, uniqueness still bites.
		user.Email = "BOB@EXAMPLE.COM"
		_, err = users.Update(context.Background(), user)
		if !errors.Is(err, model.ErrDuplicateEmail) {
			t.Fatalf("Update to a duplicate email: got %v, want ErrDuplicateEmail", err)
		}
	})

	t.Run("panics on missing password hash", func(t *testing.T) {
		seed(t)

		defer func() {
			if recover() == nil {
				t.Error("Update without a password hash must panic")
			}
		}()
		_, _ = users.Update(context.Background(), model.User{ID: 1, Version: 1})
	})
}
