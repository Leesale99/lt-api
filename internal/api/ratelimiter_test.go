package api

// Rate-limiter tests, split by layer:
//
//   - logic tests call RateLimiter.Allow/Sweep directly — no HTTP, no
//     Application, no database. The fake clock is injected via WithClock,
//     so "3 minutes of idleness" and the sweep tick are test-controlled.
//   - one middleware test drives app.rateLimit to pin the HTTP contract:
//     429, RFC-compliant integer Retry-After, error envelope.
//
// These need no database, so they run even without LT_API_TEST_DSN.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"lt-api.aleksrdvn.com/internal/constants"
)

// fakeClock is a stateful, test-controlled clock: now() returns the last
// value the test set, as many times as any goroutine asks (the request path
// and the sweep both read it). An atomic makes set/read race-free.
type fakeClock struct {
	nanos atomic.Int64
}

func newFakeClock(start time.Time) *fakeClock {
	c := &fakeClock{}
	c.nanos.Store(start.UnixNano())
	return c
}

func (c *fakeClock) set(at time.Time) {
	c.nanos.Store(at.UnixNano())
}

func (c *fakeClock) now() time.Time {
	return time.Unix(0, c.nanos.Load())
}

// newTestLimiter returns a limiter wired to the fake clock with a sweep
// period of 1ms, so a sweep fires immediately after any clock advance.
func newTestLimiter(rps float64, burst int, clock *fakeClock, opts ...Option) *RateLimiter {
	opts = append([]Option{
		WithClock(clock.now),
		WithSweepPeriod(time.Millisecond),
	}, opts...)
	return NewRateLimiter(rps, burst, true, opts...)
}

// TestRateLimiterBurstAllowed: the first burst Allow calls all succeed —
// the bucket starts full, no refill needed yet.
func TestRateLimiterBurstAllowed(t *testing.T) {
	const burst = 4
	l := newTestLimiter(2, burst, newFakeClock(time.Now()))

	for i := range burst {
		ok, _ := l.Allow("10.0.0.1")
		if !ok {
			t.Fatalf("request %d within burst: denied, want allowed", i+1)
		}
	}
}

// TestRateLimiterSustainedCapped: past the burst, requests arriving faster
// than the refill rate (1 token per 1/rps) are denied with a plausible
// retryAfter, and a genuinely refilled token is honored.
func TestRateLimiterSustainedCapped(t *testing.T) {
	const (
		rps   = 2.0
		burst = 4
	)
	l := newTestLimiter(rps, burst, newFakeClock(time.Now()))
	ip := "10.0.0.1"

	for i := range burst {
		if ok, _ := l.Allow(ip); !ok {
			t.Fatalf("warm-up request %d: denied, want allowed", i+1)
		}
	}

	// Requests arrive immediately after the burst: bucket empty, real time
	// elapsed is microseconds, so every denial's retryAfter must sit in
	// (0, 1/rps + slack] — a missing token refills within 1/rps.
	for i := range 5 {
		ok, retryAfter := l.Allow(ip)
		if ok {
			t.Fatalf("capped request %d: allowed, want denied", i+1)
		}
		max := time.Duration(1000/rps) * time.Millisecond
		if retryAfter <= 0 || retryAfter > max+50*time.Millisecond {
			t.Fatalf("capped request %d: retryAfter %v, want (0, %v]", i+1, retryAfter, max)
		}
	}

	// Wait past one refill interval: exactly one token becomes available,
	// the next request takes it, the one after is denied again.
	time.Sleep(time.Duration(1000/rps)*time.Millisecond + 100*time.Millisecond)
	if ok, _ := l.Allow(ip); !ok {
		t.Fatal("request after refill interval: denied, want allowed")
	}
	if ok, _ := l.Allow(ip); ok {
		t.Fatal("request after refill token consumed: allowed, want denied")
	}
}

// TestRateLimiterIPsIsolated: draining one client's bucket must not touch
// another client's.
func TestRateLimiterIPsIsolated(t *testing.T) {
	const burst = 3
	l := newTestLimiter(2, burst, newFakeClock(time.Now()))

	const (
		ipA = "10.0.0.1"
		ipB = "10.0.0.2"
	)

	for i := range burst {
		if ok, _ := l.Allow(ipA); !ok {
			t.Fatalf("client A request %d: denied, want allowed", i+1)
		}
	}
	if ok, _ := l.Allow(ipA); ok {
		t.Fatal("client A over burst: allowed, want denied")
	}

	// B is a fresh bucket, unaffected by A's drain.
	for i := range burst {
		if ok, _ := l.Allow(ipB); !ok {
			t.Fatalf("client B request %d (A already capped): denied, want allowed", i+1)
		}
	}
	if ok, _ := l.Allow(ipA); ok {
		t.Fatal("client A still capped: allowed, want denied")
	}
}

// TestRateLimiterStaleBucketsEvicted: the sweep drops buckets idle longer
// than the cleanup interval. The fake clock makes "3 minutes of idleness"
// instantaneous; real elapsed time stays in milliseconds, so the limiter's
// own refill cannot explain a recovery — only eviction can.
func TestRateLimiterStaleBucketsEvicted(t *testing.T) {
	const burst = 1

	clock := newFakeClock(time.Now())
	l := newTestLimiter(2, burst, clock)

	ctx := t.Context()
	go l.Sweep(ctx)

	// Consume the only token; the immediate second call is denied.
	if ok, _ := l.Allow("10.0.0.1"); !ok {
		t.Fatal("first request: denied, want allowed")
	}
	if ok, _ := l.Allow("10.0.0.1"); ok {
		t.Fatal("second request (bucket full): allowed, want denied")
	}

	// 2 minutes idle: younger than the interval, the sweep must not evict.
	clock.set(time.Now().Add(2 * time.Minute))
	if ok, _ := l.Allow("10.0.0.1"); ok {
		t.Fatal("after 2m idle: allowed, want denied (bucket must survive)")
	}

	// That Allow refreshed lastSeen to T0+2m; jump a full cleanup interval
	// past *that* (not past T0). Eviction is asynchronous: the sweep must
	// tick (1ms period) before the stale bucket is actually deleted, so
	// wait a few ticks — asserting immediately would race the sweep and hit
	// the still-mapped stale bucket.
	clock.set(time.Now().Add(2*time.Minute + constants.RateLimitCleanupInterval + time.Minute))
	time.Sleep(20 * time.Millisecond)
	if ok, _ := l.Allow("10.0.0.1"); !ok {
		t.Fatal("after >cleanup-interval idle: denied, want allowed (bucket must be evicted)")
	}
}

// okHandler marks every request that got past the limiter.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// do sends one request from the given remote address through h.
func do(h http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRateLimitMiddlewareDenial pins the HTTP contract on top of Allow: a
// denied request is a 429 with a valid integer Retry-After and the standard
// error envelope; the wrapped handler must not have run.
func TestRateLimitMiddlewareDenial(t *testing.T) {
	app := &Application{
		Logger:      quietLogger(),
		RateLimiter: NewRateLimiter(2, 1, true),
	}
	h := app.rateLimit(okHandler)

	const remoteAddr = "10.0.0.1:1111"

	if rec := do(h, remoteAddr); rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec.Code)
	}

	rec := do(h, remoteAddr)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", rec.Code)
	}

	ra := rec.Header().Get("Retry-After")
	secs, err := strconv.Atoi(ra)
	if err != nil || secs <= 0 {
		t.Fatalf("Retry-After %q, want a positive integer (RFC 7231 delay-seconds)", ra)
	}
	if rec.Body.String() == "" {
		t.Fatal("empty body, want error envelope")
	}
}

// TestRateLimitMiddlewarePassThrough: Enabled=false means the middleware is
// transparent — no 429s, no headers, at any request rate.
func TestRateLimitMiddlewarePassThrough(t *testing.T) {
	app := &Application{
		Logger:      quietLogger(),
		RateLimiter: NewRateLimiter(2, 1, false),
	}
	h := app.rateLimit(okHandler)

	for i := range 10 {
		if rec := do(h, "10.0.0.1:1111"); rec.Code != http.StatusOK {
			t.Fatalf("disabled limiter request %d: got %d, want 200", i+1, rec.Code)
		}
	}
}
