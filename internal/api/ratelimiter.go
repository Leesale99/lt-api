package api

// Standalone in-memory token-bucket rate limiter keyed by client IP.
//
// This type is deliberately HTTP-free: it exposes Allow (which answers
// "may this client proceed, and if not, for how long") and Sweep (which
// evicts stale buckets). The HTTP shell lives in middleware.go — it turns
// Allow's verdict into a 429 + Retry-After response. Keeping the logic
// HTTP-free is what lets its tests fake the clock without touching
// Application.
//
// In-memory buckets are correct while the app is a single process; the
// boundary is recorded, not built (cross-replica limiting needs a shared
// store such as Redis).
//
// Concurrency notes:
//   - one mutex guards the client map; everything touching it stays inside
//   - Reserve/Cancel is a transaction: Reserve books a token even when the
//     request is denied (possibly a future one), so denial must roll that
//     back or rejected requests would keep pushing the client's next
//     allowed slot further out
//   - the clock is injected so tests can advance "3 minutes" instantly

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"lt-api.aleksrdvn.com/internal/constants"
)

// rateLimiterClient is one IP's bucket: the token bucket itself plus the
// lastSeen timestamp the sweep uses for eviction.
type rateLimiterClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter is an in-memory token-bucket limiter keyed by client IP.
// Construct with NewRateLimiter; the zero value is not usable.
type RateLimiter struct {
	Rps     float64
	Burst   int
	Enabled bool

	// Injected clock and sweep period (production: time.Now, 1 minute).
	// Options, not exported fields: tests are the only callers that override.
	now  func() time.Time
	tick time.Duration

	mu      sync.Mutex
	clients map[string]*rateLimiterClient
}

// Option customizes a RateLimiter at construction time.
type Option func(*RateLimiter)

// WithClock overrides the clock used for lastSeen bookkeeping and stale
// eviction. Production passes nothing; tests inject a controllable fake.
func WithClock(now func() time.Time) Option {
	return func(l *RateLimiter) { l.now = now }
}

// WithSweepPeriod overrides how often the sweep checks for stale buckets.
// (The staleness threshold itself is constants.RateLimitCleanupInterval in
// both modes; only the check frequency changes.)
func WithSweepPeriod(d time.Duration) Option {
	return func(l *RateLimiter) { l.tick = d }
}

// NewRateLimiter returns a limiter allowing burst requests at once, refilled
// at rps tokens per second, per client IP.
func NewRateLimiter(rps float64, burst int, enabled bool, opts ...Option) *RateLimiter {
	l := &RateLimiter{
		Rps:     rps,
		Burst:   burst,
		Enabled: enabled,
		now:     time.Now,
		tick:    time.Minute,
		clients: make(map[string]*rateLimiterClient),
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Allow records the client's activity and reports whether the request may
// proceed. On denial it returns how long until the client's next token —
// the HTTP layer renders that as Retry-After.
func (l *RateLimiter) Allow(ip string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	client, found := l.clients[ip]
	if !found {
		client = &rateLimiterClient{
			limiter: rate.NewLimiter(rate.Limit(l.Rps), l.Burst),
		}
		l.clients[ip] = client
	}
	// Refresh on every request: an active client must never be evicted
	// mid-use, no matter how long its session lasts.
	client.lastSeen = l.now()

	res := client.limiter.Reserve()
	if !res.OK() {
		// Only reachable when the limiter is misconfigured (burst <= 0):
		// a normal denial still reserves (delay > 0). Fail closed with a
		// one-minute cooldown rather than serving unthrottled traffic.
		return false, time.Minute
	}

	delay := res.Delay()
	if delay > 0 {
		res.Cancel() // not taking the token we looked at
		return false, delay
	}
	return true, 0
}

// Sweep evicts buckets whose client has been idle longer than
// constants.RateLimitCleanupInterval. It blocks until ctx is canceled —
// callers run it in a goroutine (in production: app.background, so the
// waitgroup covers it during shutdown).
func (l *RateLimiter) Sweep(ctx context.Context) {
	ticker := time.NewTicker(l.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			for ip, client := range l.clients {
				if l.now().Sub(client.lastSeen) > constants.RateLimitCleanupInterval {
					delete(l.clients, ip)
				}
			}
			l.mu.Unlock()
		}
	}
}
