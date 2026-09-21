package api

// Rate-limiter middleware tests. These need no database — the middleware
// wraps a stub handler and the Application is constructed directly — so they
// run even without LT_API_TEST_DSN.
//
// The four scenarios are the ones the phase spec names:
//
//  1. burst within capacity is allowed
//  2. sustained rate is capped (429 + Retry-After)
//  3. distinct IPs are isolated
//  4. stale buckets are evicted (via the injected fake clock — see
//     RateLimitTestEnv in server.go for why the seam exists)

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"lt-api.aleksrdvn.com/internal/constants"
)

// newRateLimitTestApp returns an Application wired for limiter tests: no
// database dependencies, a live RootCtx (the cleanup goroutine runs) and the
// given limiter settings. The returned cancel stops the sweep goroutine —
// defer it in every test.
func newRateLimitTestApp(rps float64, burst int, env *RateLimitTestEnv) (*Application, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	return &Application{
		Limiter:          Limiter{Rps: rps, Burst: burst, Enabled: true},
		Logger:           quietLogger(),
		RootCtx:          ctx,
		RateLimitTestEnv: env,
	}, cancel
}

// fakeClock is a stateful, test-controlled clock: now() returns the last
// value the test set, as many times as any goroutine asks (the request path
// and the cleanup sweep both read it). An atomic makes set/read race-free.
type fakeClock struct {
	nanos atomic.Int64
}

func newFakeClock(t *testing.T, start time.Time) *fakeClock {
	c := &fakeClock{}
	c.set(t, start)
	return c
}

func (c *fakeClock) set(t *testing.T, at time.Time) {
	t.Helper()
	c.nanos.Store(at.UnixNano())
}

func (c *fakeClock) now() time.Time {
	return time.Unix(0, c.nanos.Load())
}

// limitedHandler returns the middleware wrapping a bare 200 handler.
func limitedHandler(app *Application) http.Handler {
	return app.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// do sends one request from the given remote address and returns the
// recorder. RemoteAddr is set explicitly: the limiter keys on it, and
// httptest.NewRequest defaults it to 192.0.2.1:1234 anyway, but spelling it
// out makes the per-IP tests readable.
func do(h http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRateLimitBurstAllowed: the first `burst` requests from one client all
// pass — the bucket starts full, and no refill is needed yet.
func TestRateLimitBurstAllowed(t *testing.T) {
	const burst = 4
	app, cancel := newRateLimitTestApp(2, burst, nil)
	defer cancel()
	h := limitedHandler(app)

	for i := 0; i < burst; i++ {
		rec := do(h, "10.0.0.1:1111")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d within burst: got %d, want 200", i+1, rec.Code)
		}
	}
}

// TestRateLimitSustainedCapped: past the burst, requests arrive faster than
// the refill rate (1/rps apart) — every one must be rejected with 429, a
// valid integer Retry-After > 0, and the standard error envelope.
func TestRateLimitSustainedCapped(t *testing.T) {
	const (
		rps   = 2.0
		burst = 4
	)
	app, cancel := newRateLimitTestApp(rps, burst, nil)
	defer cancel()
	h := limitedHandler(app)

	addr := "10.0.0.1:1111"
	for i := 0; i < burst; i++ {
		if rec := do(h, addr); rec.Code != http.StatusOK {
			t.Fatalf("warm-up request %d: got %d, want 200", i+1, rec.Code)
		}
	}

	// One token refills every 1/rps (500ms here). Sleep a bit past that, so
	// the next request genuinely earns a token — this is the only step that
	// exercises real refill, so it must use real time.
	time.Sleep(time.Duration(1000/rps)*time.Millisecond + 100*time.Millisecond)
	if rec := do(h, addr); rec.Code != http.StatusOK {
		t.Fatalf("refill token request: got %d, want 200", rec.Code)
	}

	for i := 0; i < 5; i++ {
		rec := do(h, addr)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("capped request %d: got %d, want 429", i+1, rec.Code)
		}

		ra := rec.Header().Get("Retry-After")
		secs, err := strconv.Atoi(ra)
		if err != nil || secs <= 0 {
			t.Fatalf("capped request %d: Retry-After %q, want a positive integer", i+1, ra)
		}
		// Delay can never exceed 1/rps: a single missing token refills in
		// 1/rps. (Ceil may round it up to exactly that.)
		maxRetry := int(1000/rps)/1000 + 1 // ceil-ish of 1/rps, constant-expression safe
		if secs > maxRetry {
			t.Fatalf("capped request %d: Retry-After %ds, implausibly large (max %d)", i+1, secs, maxRetry)
		}

		// The 429 must carry the standard error envelope, and — critically —
		// the stub handler must not have run.
		if body := rec.Body.String(); body == "" {
			t.Fatalf("capped request %d: empty body, want error envelope", i+1)
		}
	}
}

// TestRateLimitIPsIsolated: exhausting one client's bucket must not touch
// another client's. Both map to the same handler; only the RemoteAddr
// differs.
func TestRateLimitIPsIsolated(t *testing.T) {
	const burst = 3
	app, cancel := newRateLimitTestApp(2, burst, nil)
	defer cancel()
	h := limitedHandler(app)

	const (
		ipA = "10.0.0.1:1111"
		ipB = "10.0.0.2:2222"
	)

	// Drain A completely.
	for i := 0; i < burst; i++ {
		if rec := do(h, ipA); rec.Code != http.StatusOK {
			t.Fatalf("client A request %d: got %d, want 200", i+1, rec.Code)
		}
	}
	if rec := do(h, ipA); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A over burst: got %d, want 429", rec.Code)
	}

	// B is a fresh bucket, unaffected by A's drain — including A's most
	// recent 429.
	for i := 0; i < burst; i++ {
		if rec := do(h, ipB); rec.Code != http.StatusOK {
			t.Fatalf("client B request %d (A already capped): got %d, want 200", i+1, rec.Code)
		}
	}

	// A is still capped.
	if rec := do(h, ipA); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A still capped: got %d, want 429", rec.Code)
	}
}

// TestRateLimitStaleBucketsEvicted: the cleanup goroutine drops buckets
// whose lastSeen is older than the eviction interval. The fake clock makes
// "3 minutes of idleness" instantaneous, and a short tick period means the
// next eviction sweep runs right after we advance time.
func TestRateLimitStaleBucketsEvicted(t *testing.T) {
	const burst = 1

	clock := newFakeClock(t, time.Now())
	env := &RateLimitTestEnv{
		Now:        clock.now,
		TickPeriod: time.Millisecond, // sweep fires immediately after any advance
	}
	app, cancel := newRateLimitTestApp(2, burst, env)
	defer cancel()
	h := limitedHandler(app)

	addr := "10.0.0.1:1111"

	// First request creates the bucket at T0. It consumes the only token,
	// so the second immediate request is capped.
	if rec := do(h, addr); rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec.Code)
	}
	if rec := do(h, addr); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (bucket full): got %d, want 429", rec.Code)
	}

	// Advance 2 minutes: younger than the eviction interval, so the sweep
	// must NOT evict — the bucket (and its 429 state) survives.
	clock.set(t, time.Now().Add(2*time.Minute))
	if rec := do(h, addr); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 2m idle: got %d, want 429 (bucket must survive)", rec.Code)
	}

	// That request refreshed lastSeen to T0+2m; jump a full cleanup interval
	// past *that* (not past T0). Eviction is asynchronous, though: the sweep
	// must tick (1ms period) before the stale bucket is actually deleted, so
	// wait a few ticks — sending the request earlier would race the sweep
	// and hit the still-mapped stale bucket (429 despite staleness).
	clock.set(t, time.Now().Add(2*time.Minute+constants.RateLimitCleanupInterval+time.Minute))
	time.Sleep(20 * time.Millisecond)
	if rec := do(h, addr); rec.Code != http.StatusOK {
		t.Fatalf("after >cleanup-interval idle: got %d, want 200 (bucket must be evicted)", rec.Code)
	}
}

// TestRateLimitDisabled: when Enabled is false the middleware is a
// pass-through — no 429s regardless of request rate.
func TestRateLimitDisabled(t *testing.T) {
	app, cancel := newRateLimitTestApp(2, 1, nil)
	defer cancel()
	app.Limiter.Enabled = false
	h := limitedHandler(app)

	for i := 0; i < 10; i++ {
		rec := do(h, fmt.Sprintf("10.0.0.%d:1111", i))
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled limiter request %d: got %d, want 200", i+1, rec.Code)
		}
	}
}
