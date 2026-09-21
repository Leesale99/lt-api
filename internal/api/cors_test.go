package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// CORS tests drive the full routes() chain (recoverPanic → enableCORS →
// rateLimit → authenticate → router) against /v1/healthcheck, the one
// endpoint that runs end-to-end without touching the database — so no
// requireDB here. The allowlist is set explicitly per test: production
// gets it from config, tests own it like any other dependency.
//
// A note on "preflight fails": the server never rejects a preflight — the
// policy is silent (no CORS headers on a disallowed origin, no allowance
// for a disallowed method) and the *browser* enforces it. Server-side, a
// failing preflight asserts as "the response does not carry the
// permission the browser needs".
func TestEnableCORS(t *testing.T) {
	const (
		allowed    = "https://app.example.com"
		disallowed = "https://evil.example.com"
	)

	tests := []struct {
		name string

		method    string
		path      string
		origin    string
		reqMethod string // Access-Control-Request-Method (preflight only)

		wantCode         int
		wantAllowOrigin  string   // exact match; empty = header must be absent
		wantAllowMethods []string // empty = header must be absent
		wantHandlerRan   bool
		wantShortCircuit bool // preflight answered before the router
	}{
		{
			name:      "allowed preflight: CORS headers, short-circuits before the router",
			method:    http.MethodOptions,
			path:      "/v1/seasons/1",
			origin:    allowed,
			reqMethod: http.MethodPatch,

			wantCode:         http.StatusOK,
			wantAllowOrigin:  allowed,
			wantAllowMethods: []string{"PUT", "PATCH", "DELETE"},
			wantShortCircuit: true,
		},
		{
			name:      "disallowed preflight: no CORS headers, falls through to the router",
			method:    http.MethodOptions,
			path:      "/v1/seasons/1",
			origin:    disallowed,
			reqMethod: http.MethodPatch,

			wantCode:        http.StatusOK, // httprouter automatic OPTIONS — browser blocks on missing ACAO
			wantAllowOrigin: "",
		},
		{
			name:   "allowed origin actual GET: CORS headers present, handler ran",
			method: http.MethodGet,
			path:   "/v1/healthcheck",
			origin: allowed,

			wantCode:        http.StatusOK,
			wantAllowOrigin: allowed,
			wantHandlerRan:  true,
		},
		{
			name:   "no Origin header: no CORS headers, handler ran",
			method: http.MethodGet,
			path:   "/v1/healthcheck",

			wantCode:        http.StatusOK,
			wantAllowOrigin: "",
			wantHandlerRan:  true,
		},
		{
			name:      "preflight for non-safelisted method outside the list: no allowance",
			method:    http.MethodOptions,
			path:      "/v1/seasons/1",
			origin:    allowed,
			reqMethod: http.MethodTrace, // safelisted = GET/HEAD/POST; TRACE is neither listed nor safelisted

			wantCode:         http.StatusOK,
			wantAllowOrigin:  allowed,
			wantAllowMethods: []string{"PUT", "PATCH", "DELETE"}, // TRACE absent — browser will reject
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApplication()
			app.TrustedOrigins = []string{allowed}

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.reqMethod != "" {
				req.Header.Set("Access-Control-Request-Method", tt.reqMethod)
			}

			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
			}

			// ACAO is the permission slip: exact origin on success, absent
			// on every rejection path (never "*" — credentials forbid it).
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllowOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantAllowOrigin)
			}

			if tt.wantAllowMethods != nil {
				got := strings.Split(rr.Header().Get("Access-Control-Allow-Methods"), ", ")
				if !slices.Equal(got, tt.wantAllowMethods) {
					t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, tt.wantAllowMethods)
				}
			}

			// The preflight answer travels on headers alone; httprouter's
			// automatic-OPTIONS reply carries an Allow header, so its
			// absence proves the middleware answered before the router.
			if tt.wantShortCircuit && rr.Header().Get("Allow") != "" {
				t.Errorf("preflight reached the router: Allow = %q", rr.Header().Get("Allow"))
			}

			if tt.wantHandlerRan && !strings.Contains(rr.Body.String(), `"status": "available"`) {
				t.Errorf("handler did not run, body: %s", rr.Body.String())
			}
		})
	}
}
