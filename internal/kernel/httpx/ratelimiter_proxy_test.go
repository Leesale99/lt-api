package httpx

// Proxy-awareness tests for the rate limiter's bucket key: RateLimiter.ClientKey
// decides which address a request is throttled under, and it is the one place a
// client-supplied header can influence that decision. Every case below is a
// statement about that boundary, not about the token bucket (ratelimiter_test.go
// owns the bucket itself).
//
// The rule under test is the rightmost-untrusted-hop: with a trusted-proxy
// allowlist, the client is the last X-Forwarded-For entry that is not itself a
// trusted proxy; without one, or from an untrusted peer, the header is ignored.
//
// No database, no network: the limiter is constructed directly.

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// mustPrefixes parses the test's trusted-proxy allowlist; a typo in a test
// fixture must not silently trust nothing.
func mustPrefixes(t *testing.T, raw ...string) []netip.Prefix {
	t.Helper()

	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, r := range raw {
		prefix, err := netip.ParsePrefix(r)
		if err != nil {
			t.Fatalf("bad test fixture prefix %q: %v", r, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// doForwarded sends one request from remoteAddr with the given headers through
// h — the headers are the point, so they are set verbatim.
func doForwarded(h http.Handler, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestClientKeyTrustedProxyUsesForwardedClient: a peer inside the allowlist is
// a proxy this deployment trusts, so the header names the client and that is
// the bucket key.
func TestClientKeyTrustedProxyUsesForwardedClient(t *testing.T) {
	l := newTestLimiter(2, 1, newFakeClock(time.Now()), WithTrustedProxies(mustPrefixes(t, "10.0.0.0/8")))

	if got := l.ClientKey("10.0.0.5", []string{"203.0.113.7"}); got != "203.0.113.7" {
		t.Fatalf("ClientKey behind a trusted proxy = %q, want the forwarded client 203.0.113.7", got)
	}
}

// TestClientKeyTrustedProxyTakesRightmostUntrustedHop: a proxy chain appends
// its peer to the right, so the rightmost hop is the one nearest the API. The
// client is the rightmost hop that is not itself trusted — never the rightmost
// hop blindly (that is a trusted proxy) and never the leftmost (that is
// whatever the caller wrote).
func TestClientKeyTrustedProxyTakesRightmostUntrustedHop(t *testing.T) {
	l := newTestLimiter(2, 1, newFakeClock(time.Now()), WithTrustedProxies(mustPrefixes(t, "10.0.0.0/8")))

	tests := []struct {
		name string
		xff  string
		want string
	}{
		{
			name: "client, proxy1, proxy2 — the client is leftmost but untrusted",
			xff:  "203.0.113.7, 10.0.0.9, 10.0.0.10",
			want: "203.0.113.7",
		},
		{
			name: "untrusted intermediary sits between client and proxies",
			xff:  "203.0.113.7, 198.51.100.4, 10.0.0.10",
			want: "198.51.100.4",
		},
		{
			name: "every hop trusted — fall back to the leftmost (the origin)",
			xff:  "10.0.0.9, 10.0.0.10",
			want: "10.0.0.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := l.ClientKey("10.0.0.5", []string{tt.xff}); got != tt.want {
				t.Fatalf("ClientKey(peer 10.0.0.5, xff %q) = %q, want %q", tt.xff, got, tt.want)
			}
		})
	}
}

// TestClientKeyUntrustedPeerIgnoresForwardedFor: from a peer outside the
// allowlist the header is attacker-controlled prose. Believing it would let one
// caller burn another's bucket (or dodge its own), so the peer is the key.
func TestClientKeyUntrustedPeerIgnoresForwardedFor(t *testing.T) {
	l := newTestLimiter(2, 1, newFakeClock(time.Now()), WithTrustedProxies(mustPrefixes(t, "10.0.0.0/8")))

	got := l.ClientKey("203.0.113.50", []string{"198.51.100.4, 10.0.0.9"})
	if got != "203.0.113.50" {
		t.Fatalf("ClientKey from an untrusted peer = %q, want the peer 203.0.113.50", got)
	}
}

// TestClientKeyEmptyTrustedListIgnoresForwardedFor: the zero configuration is
// the safe one — no allowlist, no header, exactly the pre-proxy behaviour.
func TestClientKeyEmptyTrustedListIgnoresForwardedFor(t *testing.T) {
	l := newTestLimiter(2, 1, newFakeClock(time.Now()))

	for _, xff := range [][]string{nil, {"203.0.113.7"}, {"203.0.113.7, 10.0.0.9"}} {
		if got := l.ClientKey("203.0.113.50", xff); got != "203.0.113.50" {
			t.Fatalf("ClientKey with no trusted proxies and xff %v = %q, want the peer 203.0.113.50", xff, got)
		}
	}
}

// TestClientKeyMalformedForwardedForFallsBack: a trusted peer can still hand
// over a header the API cannot read (a port, a hostname, the "unknown"
// placeholder). Unparseable entries are skipped — a garbage entry cannot
// become a bucket key — and a chain with nothing parseable at all falls back to
// the peer. Note what the fallback is NOT: it is never the header's raw text.
func TestClientKeyMalformedForwardedForFallsBack(t *testing.T) {
	l := newTestLimiter(2, 1, newFakeClock(time.Now()), WithTrustedProxies(mustPrefixes(t, "10.0.0.0/8")))

	tests := []struct {
		name string
		xff  []string
		want string
	}{
		{
			name: "nothing parseable — connection peer",
			xff:  []string{"unknown"},
			want: "10.0.0.5",
		},
		{
			name: "port-qualified and hostname entries are not hops",
			xff:  []string{"203.0.113.7:5555, proxy.internal"},
			want: "10.0.0.5",
		},
		{
			name: "garbage before the real client is skipped",
			xff:  []string{"unknown, 203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name: "repeated headers are one chain",
			xff:  []string{"203.0.113.7", "10.0.0.9"},
			want: "203.0.113.7",
		},
		{
			name: "empty header value — connection peer",
			xff:  []string{"  "},
			want: "10.0.0.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := l.ClientKey("10.0.0.5", tt.xff); got != tt.want {
				t.Fatalf("ClientKey(peer 10.0.0.5, xff %v) = %q, want %q", tt.xff, got, tt.want)
			}
		})
	}
}

// TestRateLimitMiddlewareKeysOnForwardedClientBehindProxy: the end-to-end point
// of the allowlist — two clients behind ONE trusted proxy get two buckets, so
// one client's overdraw is not charged to the other, and the proxy's own
// address never becomes a key.
func TestRateLimitMiddlewareKeysOnForwardedClientBehindProxy(t *testing.T) {
	mw := &Middleware{
		Logger: quietLogger(),
		RateLimiter: NewRateLimiter(0.0001, 1, true,
			WithTrustedProxies(mustPrefixes(t, "10.0.0.0/8"))),
	}
	h := mw.RateLimit(okHandler)

	const proxy = "10.0.0.5:40000"

	// Client A spends its single token, then is refused.
	if rec := doForwarded(h, proxy, map[string]string{"X-Forwarded-For": "203.0.113.7"}); rec.Code != http.StatusOK {
		t.Fatalf("client A first request = %d, want 200", rec.Code)
	}
	if rec := doForwarded(h, proxy, map[string]string{"X-Forwarded-For": "203.0.113.7"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A second request = %d, want 429", rec.Code)
	}

	// Client B, same proxy, is a different bucket: still allowed.
	if rec := doForwarded(h, proxy, map[string]string{"X-Forwarded-For": "203.0.113.8"}); rec.Code != http.StatusOK {
		t.Fatalf("client B first request = %d, want 200 (A's overdraw must not be B's)", rec.Code)
	}
}
