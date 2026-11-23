---
id: R-KERNEL-009
title: Each client gets a token bucket, and overdraw is a 429
statement: |
  Requests are throttled per client address: a client may burst a configured number of
  requests and is then refilled at a configured rate. A client over its allowance is
  refused with a rate-limit answer that tells it how long to wait, and the operation
  behind the gate never runs.
module: kernel
entity: rate_limit
category: policy
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/ratelimiter.go::RateLimiter.Allow
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/ratelimiter.go::RateLimiter.ClientKey
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/middleware.go::Middleware.RateLimit
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/ratelimiter.go::RateLimiter.Sweep
    layer: handler
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 429
fixture: null
verification:
  unit:
    - internal/kernel/httpx/ratelimiter_test.go::TestRateLimitMiddlewareDenial
    - internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterSustainedCapped
    - internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterIPsIsolated
    - internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterStaleBucketsEvicted
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyUsesForwardedClient
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyTakesRightmostUntrustedHop
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyUntrustedPeerIgnoresForwardedFor
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyEmptyTrustedListIgnoresForwardedFor
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyMalformedForwardedForFallsBack
    - internal/kernel/httpx/ratelimiter_proxy_test.go::TestRateLimitMiddlewareKeysOnForwardedClientBehindProxy
  e2e: []
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (policy → domain): throttling is a transport decision with no domain participant. The bucket key is a client address derived from the transport (RateLimiter.ClientKey): with the trusted-proxy allowlist empty — the default, and the safe one — it is the connection peer alone and X-Forwarded-For is ignored, so a caller cannot move its own bucket by inventing a header; when the peer is inside TRUSTED_PROXIES (the Caddy reverse proxy the deployment runs in front of this process), the key is the rightmost hop of X-Forwarded-For that is not itself trusted, falling back to the leftmost hop when every hop is trusted and to the peer when no entry is parseable. Other facts the statement relies on: the denial carries Retry-After as a ceil'd whole-second count; a disabled limiter is transparent; a bucket idle longer than three minutes is evicted, and a denied request cancels its reservation so a rejection does not push the client's next slot further out. One recorded boundary, stated in code and not built: buckets are in memory, so limiting is per process — a second replica keeps its own counters, and cross-replica limiting needs a shared store (phase 07). The trusted-proxy allowlist closes the proxy collapse where every client behind a load balancer shares one bucket; the per-process fact is unchanged.
