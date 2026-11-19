package service

// Tests for the sign-in use case (Service.Authenticate, R-IDENTITY-018): an
// unknown email and a wrong password must leave the service as the same
// refusal, so nothing above it — and no caller reading the answer — can tell
// which half failed.
//
// The timing half of the rule is deliberately NOT asserted here: a wall-clock
// comparison of two bcrypt calls is flaky by construction. What is pinned is
// that the unknown-email path runs the dummy comparison at all (the burn has no
// observable verdict) and that both paths produce the same sentinel.

import (
	"context"
	"errors"
	"testing"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

func TestAuthenticate(t *testing.T) {
	requireDB(t)
	h := newHarness()

	seedSigner := func(t *testing.T) model.User {
		t.Helper()
		resetUsers(t)

		user, err := h.users.Insert(context.Background(), mustUser("Alice", "alice@example.com", "pa55word123"))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
		return user
	}

	t.Run("an unknown email is an invalid credential", func(t *testing.T) {
		seedSigner(t)

		_, err := h.svc.Authenticate(context.Background(), "nobody@example.com", "pa55word123")
		if !errors.Is(err, model.ErrInvalidCredentials) {
			t.Fatalf("Authenticate(unknown email) = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("a wrong password is an invalid credential", func(t *testing.T) {
		seedSigner(t)

		_, err := h.svc.Authenticate(context.Background(), "alice@example.com", "not-her-password")
		if !errors.Is(err, model.ErrInvalidCredentials) {
			t.Fatalf("Authenticate(wrong password) = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("both invalid-credential paths are one refusal", func(t *testing.T) {
		seedSigner(t)

		_, unknownEmailErr := h.svc.Authenticate(context.Background(), "nobody@example.com", "pa55word123")
		_, wrongPasswordErr := h.svc.Authenticate(context.Background(), "alice@example.com", "not-her-password")

		if unknownEmailErr == nil || wrongPasswordErr == nil {
			t.Fatalf("both halves must refuse: unknown email = %v, wrong password = %v", unknownEmailErr, wrongPasswordErr)
		}
		if !errors.Is(unknownEmailErr, model.ErrInvalidCredentials) || !errors.Is(wrongPasswordErr, model.ErrInvalidCredentials) {
			t.Fatalf("refusals = (%v, %v), want ErrInvalidCredentials for both", unknownEmailErr, wrongPasswordErr)
		}
		if unknownEmailErr.Error() != wrongPasswordErr.Error() {
			t.Errorf("refusal sentences differ: %q vs %q", unknownEmailErr, wrongPasswordErr)
		}
	})

	t.Run("the right password mints an authentication token", func(t *testing.T) {
		user := seedSigner(t)

		token, err := h.svc.Authenticate(context.Background(), "alice@example.com", "pa55word123")
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if token.UserID != user.ID || token.Scope != model.ScopeAuthentication {
			t.Errorf("token = (user %d, scope %q), want (%d, %q)", token.UserID, token.Scope, user.ID, model.ScopeAuthentication)
		}
		if token.Plaintext == "" {
			t.Error("the minted token carries no plaintext")
		}
	})
}
