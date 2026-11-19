package httpchain

// The chain's ORDER is its whole content, so this test observes the order
// from outside rather than inspecting the composition: each case fails when
// two rungs swap. Real httpx middleware, a real limiter, a stub credential
// lookup — no module and no database.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// rejectingUsers is the credential seam's refusal: every bearer token is
// malformed/unknown, so Authenticate answers 401 whenever it gets to run.
type rejectingUsers struct{}

func (rejectingUsers) UserForToken(context.Context, string) (httpx.AuthUser, error) {
	return httpx.AuthUser{}, httpx.ErrInvalidToken
}

// newChain builds the chain under test with an allowlist, a one-token bucket
// and the refusing credential seam. rps is small enough that no token is
// refilled during the test.
func newChain(t *testing.T, origins []string) http.Handler {
	t.Helper()

	router := http.NewServeMux()
	router.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	router.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	chain := &httpx.Middleware{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TrustedOrigins: origins,
		RateLimiter:    httpx.NewRateLimiter(0.0001, 1, true),
		Users:          rejectingUsers{},
	}

	return Build(chain, router)
}

// get issues one request through h and returns the recorder.
func do(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestBuildOrder pins the nesting RecoverPanic → EnableCORS → RateLimit →
// Authenticate → router. Each case is one adjacent pair in that order, so any
// swap of two rungs turns one of them red.
func TestBuildOrder(t *testing.T) {
	allowed := "https://app.example"

	t.Run("recovery is outermost: a panicking route is answered", func(t *testing.T) {
		rr := do(newChain(t, nil), http.MethodGet, "/panic", nil)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("GET /panic = %d, want 500 (RecoverPanic must wrap the chain)", rr.Code)
		}
	})

	t.Run("cross-origin handling is inside recovery but outside throttling", func(t *testing.T) {
		h := newChain(t, []string{allowed})

		// A preflight is answered by CORS before the limiter looks at it: the
		// single token of the bucket is still spendable afterwards.
		preflight := do(h, http.MethodOptions, "/ok", map[string]string{
			"Origin":                        allowed,
			"Access-Control-Request-Method": "POST",
		})
		if preflight.Code != http.StatusOK {
			t.Fatalf("preflight = %d, want 200 (EnableCORS must wrap RateLimit)", preflight.Code)
		}
		if got := preflight.Header().Get("Access-Control-Allow-Origin"); got != allowed {
			t.Errorf("preflight Allow-Origin = %q, want %q", got, allowed)
		}

		// The preflight spent nothing, so the first real request passes…
		if rr := do(h, http.MethodGet, "/ok", nil); rr.Code != http.StatusOK {
			t.Fatalf("first GET = %d, want 200 (preflight must not spend a token)", rr.Code)
		}
		// …and the second is the bucket's refusal.
		if rr := do(h, http.MethodGet, "/ok", nil); rr.Code != http.StatusTooManyRequests {
			t.Fatalf("second GET = %d, want 429 (the limiter is real)", rr.Code)
		}
	})

	t.Run("throttling is outside credential resolution", func(t *testing.T) {
		h := newChain(t, nil)
		badAuth := map[string]string{"Authorization": "Bearer nonsense"}

		if rr := do(h, http.MethodGet, "/ok", nil); rr.Code != http.StatusOK {
			t.Fatalf("first GET = %d, want 200", rr.Code)
		}
		// The bucket is empty: the answer must be the throttle, not the
		// credential refusal — RateLimit wraps Authenticate.
		if rr := do(h, http.MethodGet, "/ok", badAuth); rr.Code != http.StatusTooManyRequests {
			t.Fatalf("throttled request with a bad credential = %d, want 429 (RateLimit must wrap Authenticate)", rr.Code)
		}
	})

	t.Run("credential resolution is outside the router", func(t *testing.T) {
		h := newChain(t, nil)
		// A path the router does not serve: a bad credential is refused
		// before the router's own 404 writer sees it.
		rr := do(h, http.MethodGet, "/missing", map[string]string{"Authorization": "Bearer nonsense"})
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("GET /missing with a bad credential = %d, want 401 (Authenticate must wrap the router)", rr.Code)
		}
	})
}
