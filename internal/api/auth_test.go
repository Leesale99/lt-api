package api

// Tests for the authentication/authorization wall: app.authenticate +
// app.requirePermission, exercised end-to-end through routes() against the
// real fixture from seed_test.go.
//
// Fixture contract (see resetSQL): four authentication tokens with fixed
// plaintexts — admin (activated, role 'admin'), user (activated, role
// 'user'), pending (role 'user', NOT activated) and one expired token. The
// plaintexts are the SQL-side SHA-256 hashes' preimages; only the hashes are
// stored, mirroring the production invariant.
//
// Endpoint choice: GET/POST /v1/teams. It needs no body and no fixture rows,
// and 'user' holds teams:read but not teams:write, so one endpoint exercises
// the whole gate ladder.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	// 26 chars each: the length ValidateUserTokenPlaintext demands. Only
	// their SHA-256 lives in the fixture (seed_test.go).
	adminAuthToken   = "aaaaaaaaaaaaaaaaaaaaaaaaaa" // user 1: activated admin
	userAuthToken    = "bbbbbbbbbbbbbbbbbbbbbbbbbb" // user 2: activated 'user'
	expiredAuthToken = "cccccccccccccccccccccccccc" // user 1, expiry in the past
	pendingAuthToken = "dddddddddddddddddddddddddd" // user 3: unactivated 'user'
)

// withAuth attaches a Bearer token to a request under construction. Existing
// domain tests pass adminAuthToken (the superuser path); auth-specific tests
// pick their own token.
func withAuth(req *http.Request, token string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestAuthenticateMiddleware(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		header   string // raw Authorization value; empty = header absent
		wantCode int
		wantBody []string
	}{
		{
			name:     "no header passes through anonymously",
			header:   "",
			wantCode: http.StatusUnauthorized, // blocked downstream by requirePermission
			wantBody: []string{"you must be authenticated"},
		},
		{
			name:     "malformed scheme",
			header:   "Token " + adminAuthToken,
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "no scheme",
			header:   adminAuthToken,
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "three header parts",
			header:   "Bearer extra " + adminAuthToken,
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "token fails plaintext validation (too short)",
			header:   "Bearer short",
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "expired token is indistinguishable from unknown",
			header:   "Bearer " + expiredAuthToken,
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "valid-format token that does not exist",
			header:   "Bearer zzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantCode: http.StatusUnauthorized,
			wantBody: []string{"invalid or missing authentication"},
		},
		{
			name:     "valid admin token authenticates",
			header:   "Bearer " + adminAuthToken,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			req := httptest.NewRequest(http.MethodGet, "/v1/teams", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
			}
			if vary := rr.Header().Get("Vary"); vary != "Authorization" {
				t.Errorf("Vary header = %q, want %q", vary, "Authorization")
			}
			for _, fragment := range tt.wantBody {
				if !strings.Contains(rr.Body.String(), fragment) {
					t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
				}
			}
			// WWW-Authenticate must ride the 401s from the token path
			// (invalidAuthenticationTokenResponse), but NOT the anonymous
			// pass-through (authenticationRequiredResponse).
			if wwwAuth := rr.Header().Get("WWW-Authenticate"); wwwAuth != "Bearer" && tt.wantCode == http.StatusUnauthorized && tt.header != "" {
				t.Errorf("WWW-Authenticate header = %q, want %q", wwwAuth, "Bearer")
			}
		})
	}
}

func TestRequirePermissionMiddleware(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		method   string
		url      string
		token    string
		wantCode int
		wantBody []string
	}{
		{
			name:     "unactivated user with valid token",
			method:   http.MethodGet,
			url:      "/v1/teams",
			token:    pendingAuthToken,
			wantCode: http.StatusForbidden,
			wantBody: []string{"must be activated"},
		},
		{
			name:     "activated user, read permission granted",
			method:   http.MethodGet,
			url:      "/v1/teams",
			token:    userAuthToken,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`},
		},
		{
			name:     "activated user, write permission denied",
			method:   http.MethodPost,
			url:      "/v1/teams",
			token:    userAuthToken,
			wantCode: http.StatusForbidden,
			wantBody: []string{"doesn't have the necessary permissions"},
		},
		{
			name:     "admin passes the write gate",
			method:   http.MethodPost,
			url:      "/v1/teams",
			token:    adminAuthToken,
			wantCode: http.StatusUnprocessableEntity, // route reached: validation error on empty body
			wantBody: []string{"must be provided"},  // anything but the 403s above proves the gate opened
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			var req *http.Request
			if tt.method == http.MethodPost {
				req = httptest.NewRequest(tt.method, tt.url, strings.NewReader(`{}`))
			} else {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			}
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, withAuth(req, tt.token))

			if rr.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
			}
			for _, fragment := range tt.wantBody {
				if !strings.Contains(rr.Body.String(), fragment) {
					t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
				}
			}
		})
	}
}
