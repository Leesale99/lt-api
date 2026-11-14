package model

// Tests for the user entity's validators and the bcrypt password round-trip.
// Pure unit tests: no database, no transport.
//
// The storage behaviour these rules guard (Insert/GetByEmail/Update, the
// duplicate-email translation, the optimistic-concurrency write) is tested in
// the repository package.

import (
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/validator"
)

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
