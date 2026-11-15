package model

// Tests for the token generator — pure unit tests, no database. The storage
// behaviour (New/GetForToken/DeleteAllForUser) is tested in the repository
// package.

import (
	"crypto/sha256"
	"slices"
	"testing"
	"time"
)

func TestGenerateUserToken(t *testing.T) {
	t.Run("plaintext is exactly 26 characters", func(t *testing.T) {
		token := GenerateUserToken(1, time.Hour, ScopeAuthentication)
		if len(token.Plaintext) != plaintextTokenLength {
			t.Errorf("plaintext length = %d, want %d", len(token.Plaintext), plaintextTokenLength)
		}
	})

	t.Run("hash is the SHA-256 of the plaintext", func(t *testing.T) {
		token := GenerateUserToken(1, time.Hour, ScopeAuthentication)
		want := sha256.Sum256([]byte(token.Plaintext))
		if !slices.Equal(token.Hash, want[:]) {
			t.Error("token.Hash does not equal SHA-256(plaintext)")
		}
	})

	t.Run("expiry honours the ttl", func(t *testing.T) {
		ttl := 2 * time.Hour
		token := GenerateUserToken(1, ttl, ScopeAuthentication)
		// Clock can tick between generation and assertion; allow a minute.
		if !token.Expiry.After(time.Now().Add(ttl - time.Minute)) {
			t.Errorf("expiry = %v, want ~now + %v", token.Expiry, ttl)
		}
	})

	t.Run("scope and user are carried", func(t *testing.T) {
		token := GenerateUserToken(42, time.Hour, ScopeActivation)
		if token.UserID != 42 || token.Scope != ScopeActivation {
			t.Errorf("token = (user %d, scope %q), want (42, %q)", token.UserID, token.Scope, ScopeActivation)
		}
	})

	t.Run("two generations never collide", func(t *testing.T) {
		first := GenerateUserToken(1, time.Hour, ScopeAuthentication)
		second := GenerateUserToken(1, time.Hour, ScopeAuthentication)
		if first.Plaintext == second.Plaintext {
			t.Error("two tokens share a plaintext: crypto/rand reuse")
		}
	})
}
