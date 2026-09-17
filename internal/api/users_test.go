package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Tests for POST /v1/users (registerUser). These run against the real test
// database, so they also cover the identity.UsersStore.Insert paths the
// handler depends on — most importantly the duplicate-email translation
// (pg 23505 on users_email_key → ErrDuplicateEmail → 422), which was
// previously a nil-pointer bug (pqErr never populated via errors.As).

// registerUserCase is the table row shape shared by the handler tests.
type registerUserCase struct {
	name     string
	body     string
	wantCode int
	wantBody []string
}

// lastRegisterBody holds the response body of the most recent
// runRegisterUserCase call, for cases that need deeper assertions than the
// table fragments.
var lastRegisterBody string

func runRegisterUserCase(t *testing.T, tt registerUserCase) {
	t.Helper()

	app := newTestApplication()

	var reader io.Reader
	if tt.body != "" {
		reader = strings.NewReader(tt.body)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/users", reader)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != tt.wantCode {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
	}
	lastRegisterBody = rr.Body.String()
	for _, fragment := range tt.wantBody {
		if !strings.Contains(rr.Body.String(), fragment) {
			t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
		}
	}
}

func TestRegisterUserHandler(t *testing.T) {
	requireDB(t)

	tests := []registerUserCase{
		{
			name:     "valid registration",
			body:     `{"name":"Alice Example","email":"alice@example.com","password":"pa55word123"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{
				`"name": "Alice Example"`,
				`"email": "alice@example.com"`,
				`"activated": false`,
			},
		},
		{
			// encoding/json matches keys case-insensitively (equalFold), and
			// DisallowUnknownFields only rejects keys matching NO field — so
			// the capital-E variant binds to the same field and is accepted.
			// This test pins that quirk: enforcing exact key case would
			// require a custom Decode hook, not just a tag change.
			name:     "capital-E Email key still binds (encoding/json case-insensitivity)",
			body:     `{"name":"X","Email":"x@example.com","password":"pa55word123"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"email": "x@example.com"`},
		},
		{
			// no pre-existing user in a fresh subtest (reset truncates users);
			// the duplicate-email clash is covered by the dedicated test below.
			name:     "second distinct email registers fine",
			body:     `{"name":"Alice Again","email":"alice2@example.com","password":"pa55word123"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"email": "alice2@example.com"`},
		},
		{
			name:     "missing name",
			body:     `{"email":"bob@example.com","password":"pa55word123"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name", "must be provided"},
		},
		{
			name:     "invalid email format",
			body:     `{"name":"Bob","email":"not-an-email","password":"pa55word123"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"email", "must be a valid email address"},
		},
		{
			name:     "password too short",
			body:     `{"name":"Bob","email":"bob@example.com","password":"short"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"password", "must be at least 8 bytes long"},
		},
		{
			// bcrypt truncates input at 72 bytes, so the validator must
			// reject longer passwords instead of letting two passwords
			// sharing the first 72 bytes collide.
			name:     "password over 72 bytes",
			body:     fmt.Sprintf(`{"name":"Bob","email":"bob@example.com","password":"%s"}`, strings.Repeat("p", 73)),
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"password", "must not be more then 72 bytes long"},
		},
		{
			name:     "empty body",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			body:     `{"name":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			// id and activated are server-controlled: the client must not be
			// able to pick an identity or self-activate.
			name:     "unknown fields rejected",
			body:     `{"name":"X","email":"x@example.com","password":"pa55word123","activated":true,"id":5}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			runRegisterUserCase(t, tt)
		})
	}
}

// TestRegisterUserHandlerSuccessState verifies what the 201 body alone
// cannot prove: the Location header, that no plaintext password ever reached
// the database, and that the stored hash actually matches the password the
// user registered with.
func TestRegisterUserHandlerSuccessState(t *testing.T) {
	requireDB(t)

	reset(t)

	app := newTestApplication()
	body := `{"name":"Alice Example","email":"alice@example.com","password":"pa55word123"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/users", strings.NewReader(body))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/v1/users/1" {
		t.Errorf("Location header = %q, want %q", loc, "/v1/users/1")
	}
	// Password must never appear in the response or the database.
	for _, leak := range []string{"pa55word123", "password_hash"} {
		if strings.Contains(rr.Body.String(), leak) {
			t.Errorf("response leaks %q (body: %s)", leak, rr.Body.String())
		}
	}

	var (
		storedHash []byte
		activated  bool
		version    int
	)
	err := testPool.QueryRow(context.Background(),
		`SELECT password_hash, activated, version FROM users WHERE email = 'alice@example.com'`).
		Scan(&storedHash, &activated, &version)
	if err != nil {
		t.Fatalf("registered user not found in database: %v", err)
	}
	if string(storedHash) == "pa55word123" {
		t.Fatal("plaintext password stored in password_hash")
	}
	if err := bcrypt.CompareHashAndPassword(storedHash, []byte("pa55word123")); err != nil {
		t.Errorf("stored hash does not match the registered password: %v", err)
	}
	if activated {
		t.Error("new user must not be activated")
	}
	if version != 1 {
		t.Errorf("new user version = %d, want 1", version)
	}
}

// TestRegisterUserHandlerDuplicateIsolatedFromValidation pins the exact
// translation chain of the duplicate-email path: exactly one error on the
// email field, HTTP 422 — the path that previously dereferenced a nil
// *pgconn.PgError and would have panicked at runtime.
func TestRegisterUserHandlerDuplicateIsolatedFromValidation(t *testing.T) {
	requireDB(t)

	reset(t)
	runRegisterUserCase(t, registerUserCase{
		name:     "seed a user",
		body:     `{"name":"Alice","email":"alice@example.com","password":"pa55word123"}`,
		wantCode: http.StatusCreated,
	})
	runRegisterUserCase(t, registerUserCase{
		// Fully valid body, same email in a different case: citext + UNIQUE
		// must translate to exactly one error keyed "email" — the path that
		// previously dereferenced a nil *pgconn.PgError and would have
		// panicked at runtime.
		name:     "duplicate email yields exactly the email error",
		body:     `{"name":"Alice Again","email":"ALICE@EXAMPLE.COM","password":"pa55word123"}`,
		wantCode: http.StatusUnprocessableEntity,
		wantBody: []string{"a user with this email address already exists"},
	})
	if strings.Contains(lastRegisterBody, "must be provided") {
		t.Errorf("duplicate path must report only the email error, got: %s", lastRegisterBody)
	}
}
