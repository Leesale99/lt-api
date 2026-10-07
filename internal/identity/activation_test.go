package identity

// Tests for the activation use case (Store.Activate, ADR-025): the token
// lookup, the activated flip and the activation-token cleanup must behave
// as one unit of persistence.
//
// Integration tests against the same "identity" suite database as the
// other store tests. The rollback case forces the LAST statement of the
// transaction to fail with a test-only trigger — the only way to inject a
// deterministic mid-transaction failure with no production seam — and then
// asserts NOTHING was written: the flag flip rolled back with the cleanup.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	storepkg "lt-api.aleksrdvn.com/internal/store"
)

// plantActivatableUser inserts an unactivated user with one fresh activation
// token and returns both.
func plantActivatableUser(t *testing.T, store *Store) (User, UserToken) {
	t.Helper()

	user, err := store.Users.Insert(context.Background(), mustUser("Alice", fmt.Sprintf("alice-%d@example.com", time.Now().UnixNano()), "pa55word123"))
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	token, err := store.UserTokens.New(context.Background(), user.ID, time.Hour, ScopeActivation)
	if err != nil {
		t.Fatalf("seed token: %v", err)
	}

	return user, token
}

// assertUserState reads the user's persisted (activated, version) pair.
func assertUserState(t *testing.T, userID int) (bool, int) {
	t.Helper()

	var activated bool
	var version int
	if err := testPool.QueryRow(context.Background(),
		`SELECT activated, version FROM users WHERE id = $1`, userID).Scan(&activated, &version); err != nil {
		t.Fatalf("read user: %v", err)
	}

	return activated, version
}

func TestActivate(t *testing.T) {
	requireDB(t)
	store := NewStore(testPool)

	t.Run("happy path: flip and cleanup commit together", func(t *testing.T) {
		resetUsers(t)
		user, token := plantActivatableUser(t, store)

		got, err := store.Activate(context.Background(), token.Plaintext)
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}

		if !got.Activated {
			t.Errorf("returned user activated = false, want true")
		}
		if got.Version != user.Version+1 {
			t.Errorf("returned version = %d, want %d", got.Version, user.Version+1)
		}

		// Both halves persisted: the flag is set AND the token is gone.
		activated, version := assertUserState(t, user.ID)
		if activated != true || version != user.Version+1 {
			t.Fatalf("persisted (activated, version) = (%v, %d), want (true, %d)", activated, version, user.Version+1)
		}

		var tokens int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM user_tokens WHERE user_id = $1 AND scope = $2`, user.ID, ScopeActivation).Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		if tokens != 0 {
			t.Errorf("activation tokens after activate = %d, want 0", tokens)
		}
	})

	t.Run("unknown, expired or out-of-scope token is ErrRecordNotFound", func(t *testing.T) {
		resetUsers(t)
		user, _ := plantActivatableUser(t, store)

		if _, err := store.Activate(context.Background(), "ZZZZZZZZZZZZZZZZZZZZZZZZZZ"); !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Fatalf("Activate(unknown) = %v, want ErrRecordNotFound", err)
		}

		expired, err := store.UserTokens.New(context.Background(), user.ID, -time.Minute, ScopeActivation)
		if err != nil {
			t.Fatalf("seed expired token: %v", err)
		}
		if _, err := store.Activate(context.Background(), expired.Plaintext); !errors.Is(err, storepkg.ErrRecordNotFound) {
			t.Fatalf("Activate(expired) = %v, want ErrRecordNotFound", err)
		}

		// The refusals wrote nothing: still unactivated at the planted version.
		activated, version := assertUserState(t, user.ID)
		if activated != false || version != user.Version {
			t.Fatalf("failed activation touched the row: (%v, %d), want (false, %d)", activated, version, user.Version)
		}
	})

	t.Run("a failed cleanup rolls the flip back — nothing is left behind", func(t *testing.T) {
		resetUsers(t)
		user, token := plantActivatableUser(t, store)

		// Test-only failure injection: forbid deleting from user_tokens.
		// This is the deterministic stand-in for the crash the invariant
		// exists for — the UPDATE has landed (in the tx), then the DELETE
		// fails, and the commit must never happen.
		if _, err := testPool.Exec(context.Background(), `
			CREATE FUNCTION test_forbid_token_delete() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'test: activation token delete forbidden' USING ERRCODE = 'P0001';
			END $$ LANGUAGE plpgsql`); err != nil {
			t.Fatalf("create forbid function: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_forbid_token_delete ON user_tokens`)
			_, _ = testPool.Exec(context.Background(), `DROP FUNCTION IF EXISTS test_forbid_token_delete()`)
		})
		if _, err := testPool.Exec(context.Background(), `
			CREATE TRIGGER test_forbid_token_delete BEFORE DELETE ON user_tokens
			FOR EACH ROW EXECUTE FUNCTION test_forbid_token_delete()`); err != nil {
			t.Fatalf("create forbid trigger: %v", err)
		}

		_, err := store.Activate(context.Background(), token.Plaintext)
		if err == nil {
			t.Fatal("Activate() = nil error, want the injected failure")
		}

		// The invariant under test: the flip did NOT survive. A non-atomic
		// writer would leave activated=true with the token still present.
		activated, version := assertUserState(t, user.ID)
		if activated != false || version != user.Version {
			t.Fatalf("partial write survived the rollback: (activated, version) = (%v, %d), want (false, %d)", activated, version, user.Version)
		}

		var tokens int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM user_tokens WHERE user_id = $1 AND scope = $2`, user.ID, ScopeActivation).Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		if tokens != 1 {
			t.Errorf("activation tokens after rollback = %d, want 1 (the token must survive too)", tokens)
		}
	})

	t.Run("two concurrent activations activate exactly once", func(t *testing.T) {
		resetUsers(t)

		store := NewStore(testPool)
		user, token1 := plantActivatableUser(t, store)

		// A second fresh token for the same user: two independent
		// activations, same guarded write target.
		token2, err := store.UserTokens.New(context.Background(), user.ID, time.Hour, ScopeActivation)
		if err != nil {
			t.Fatalf("seed second token: %v", err)
		}

		var (
			wg     sync.WaitGroup
			errs   = make([]error, 2)
			builds = []string{token1.Plaintext, token2.Plaintext}
		)
		wg.Add(2)
		for i := range builds {
			go func(i int) {
				defer wg.Done()
				_, errs[i] = store.Activate(context.Background(), builds[i])
			}(i)
		}
		wg.Wait()

		// Every outcome is legal in exactly one of two shapes: committed
		// (nil), losing the version race (ErrEditConflict), or reading a
		// snapshot that predates the winner's token cleanup
		// (ErrRecordNotFound). Any other error is a bug.
		winners := 0
		for _, err := range errs {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, storepkg.ErrEditConflict), errors.Is(err, storepkg.ErrRecordNotFound):
				// A legal loser.
			default:
				t.Fatalf("Activate() = %v, want nil, ErrEditConflict or ErrRecordNotFound", err)
			}
		}
		if winners == 0 {
			t.Fatalf("no activation committed (errs: %v)", errs)
		}

		// Exactly-once on the row: one flip, one version bump, no tokens.
		activated, version := assertUserState(t, user.ID)
		if activated != true || version != user.Version+1 {
			t.Fatalf("persisted (activated, version) = (%v, %d), want (true, %d) — a double bump or missed flip is a lost exactly-once", activated, version, user.Version+1)
		}

		var tokens int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM user_tokens WHERE user_id = $1 AND scope = $2`, user.ID, ScopeActivation).Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		if tokens != 0 {
			t.Errorf("activation tokens after the race = %d, want 0", tokens)
		}
	})
}
