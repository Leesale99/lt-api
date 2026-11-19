package idempotency

// Unit test for the token validator: pure logic, no database. The same rule
// the DB's idempotency_keys_key_check enforces as the backstop (key is a
// non-empty string of at most 255 bytes: octet_length <= 255).
//
// The store's behavior (claim, replay, conflict, rollback) is exercised
// through the game service's ride-command tests (internal/game), which run
// against the real table — the store has no unit tests of its own.

import (
	"crypto/sha256"
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// testToken builds a valid token: the hash must be SHA-256-sized (32 bytes),
// matching the idempotency_keys request_hash check. The claim row is
// (key, user_id), so the token names its caller (R-KERNEL-003).
func testToken(key, endpoint string) IdempotencyToken {
	hash := sha256.Sum256([]byte(key))
	return IdempotencyToken{
		Key:      key,
		UserID:   7,
		Endpoint: endpoint,
		Hash:     hash[:],
	}
}

func TestValidateIdempotencyToken(t *testing.T) {
	valid := func() IdempotencyToken {
		return testToken("replay-key", "POST /v1/rides/:id/lock")
	}

	tests := []struct {
		name     string
		mutate   func(t *IdempotencyToken)
		wantErrs map[string]string // field -> first error message; empty = valid
	}{
		{
			name:     "valid token",
			mutate:   func(t *IdempotencyToken) {},
			wantErrs: map[string]string{},
		},
		{
			name:     "empty key is rejected",
			mutate:   func(t *IdempotencyToken) { t.Key = "" },
			wantErrs: map[string]string{"idempotency_key": "must be provided"},
		},
		{
			name:     "key over 255 bytes is rejected",
			mutate:   func(t *IdempotencyToken) { t.Key = strings.Repeat("k", 256) },
			wantErrs: map[string]string{"idempotency_key": "must not be more than 255 bytes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := valid()
			tt.mutate(&token)

			v := validator.New()
			ValidateIdempotencyToken(v, token)

			if len(tt.wantErrs) == 0 {
				if !v.Valid() {
					t.Fatalf("ValidateIdempotencyToken() = %v, want no errors", v.Errors)
				}
				return
			}

			if v.Valid() {
				t.Fatal("ValidateIdempotencyToken() = valid, want errors")
			}
			for field, want := range tt.wantErrs {
				if got, ok := v.Errors[field]; !ok || got != want {
					t.Errorf("errors[%q] = %q (present: %v), want %q", field, got, ok, want)
				}
			}
		})
	}
}
