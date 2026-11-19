package httpx

// Standalone in-memory token-bucket rate limiter keyed by client IP.
//
// This type is deliberately HTTP-free: it exposes Allow (which answers
// "may this client proceed, and if not, for how long") and Sweep (which
// evicts stale buckets). The HTTP shell lives in middleware.go — it turns
// Allow's verdict into a 429 + Retry-After response. Keeping the logic
// HTTP-free is what lets its tests fake the clock without touching
// Application.
//
// Buckets are in memory, so limiting is per process; that boundary is
// recorded, not built (cross-replica limiting needs a shared store such as
// Redis). Buckets are keyed by the client address, which by default is the
// connection peer; a deployment behind a reverse proxy declares the proxy in
// WithTrustedProxies and the key becomes the client the proxy forwards for
// (see ClientKey).
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
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
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

	// trustedProxies is the reverse-proxy allowlist (WithTrustedProxies).
	// Empty is the safe default: the bucket key is the connection peer and
	// X-Forwarded-For is ignored.
	trustedProxies []netip.Prefix

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
// (The staleness threshold itself is RateLimitCleanupInterval in
// both modes; only the check frequency changes.)
func WithSweepPeriod(d time.Duration) Option {
	return func(l *RateLimiter) { l.tick = d }
}

// WithTrustedProxies declares the peers whose X-Forwarded-For may name the
// client: a peer inside one of these prefixes is a reverse proxy this
// deployment trusts (in production: the Caddy instance in front of the API).
// The empty list — the default — trusts nobody, which is exactly the
// connection-peer-only behaviour. The prefixes come from TRUSTED_PROXIES
// (platform config validates them at boot).
func WithTrustedProxies(prefixes []netip.Prefix) Option {
	return func(l *RateLimiter) {
		l.trustedProxies = append([]netip.Prefix(nil), prefixes...)
	}
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

// ClientKey derives the bucket key for one request from its transport facts:
// peer is the host part of r.RemoteAddr and forwardedFor carries the request's
// X-Forwarded-For values verbatim (net/http splits repeated headers for us).
//
// The rule is the RIGHTMOST-UNTRUSTED-HOP: the client is the last address in
// the forwarded chain that is not itself a trusted proxy — everything to its
// right was appended by a proxy we trust, everything to its left is whatever
// the caller claimed. Consequences:
//   - the trusted list empty (the default) or the peer outside it: the peer is
//     the client and the header is ignored entirely, so an untrusted caller
//     cannot move its own bucket by inventing an X-Forwarded-For;
//   - the peer trusted (the Caddy reverse proxy the deployment runs in front
//     of this process): walk the hops right to left and take the first
//     untrusted one; if every hop is trusted, the leftmost hop is the
//     originating client;
//   - unparseable entries are skipped, and a chain with no parseable address
//     at all falls back to the peer — a mangled header never merges clients,
//     it only withholds the more precise key.
func (l *RateLimiter) ClientKey(peer string, forwardedFor []string) string {
	if len(l.trustedProxies) == 0 || !l.trusted(peer) {
		return peer
	}

	var hops []string
	for _, header := range forwardedFor {
		for raw := range strings.SplitSeq(header, ",") {
			if addr, ok := parseIP(raw); ok {
				hops = append(hops, addr)
			}
		}
	}
	if len(hops) == 0 {
		return peer
	}

	for i := len(hops) - 1; i >= 0; i-- {
		if !l.trusted(hops[i]) {
			return hops[i]
		}
	}
	return hops[0]
}

// trusted reports whether addr is a reverse proxy this deployment trusts.
// An unparseable address is never trusted, which keeps the fallback on the
// connection peer.
func (l *RateLimiter) trusted(addr string) bool {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range l.trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// parseIP canonicalizes one X-Forwarded-For entry. Only a bare IP is a hop:
// a port, a hostname or a "unknown" placeholder is not, and is skipped.
func parseIP(raw string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
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
// RateLimitCleanupInterval. It blocks until ctx is canceled —
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
				if l.now().Sub(client.lastSeen) > RateLimitCleanupInterval {
					delete(l.clients, ip)
				}
			}
			l.mu.Unlock()
		}
	}
}
