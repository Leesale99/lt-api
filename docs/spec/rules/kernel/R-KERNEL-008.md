---
id: R-KERNEL-008
title: A malformed credential is refused before the handler
statement: |
  A request that presents a credential must present it as a bearer token; anything
  else — an unknown scheme, a missing value — is refused as unauthenticated before any
  handler runs. A request that presents no credential at all is not refused here: it
  proceeds as an anonymous caller and is judged by the permission gate.
module: kernel
entity: http_request
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/middleware.go::Middleware.Authenticate
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/errors.go::InvalidAuthenticationToken
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/context.go::SetAuthenticatedUser
    layer: handler
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidToken
  http: 401
fixture: null
verification:
  unit: null
  e2e:
    - internal/e2e/auth_test.go::TestAuthenticateMiddleware
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

One ladder, one rule: format first (two space-separated parts, scheme Bearer), then resolution (the consumer-side UserLookup answers ErrInvalidToken for malformed, unknown and expired alike — the middleware never explains which), and an unresolved transport error is a 500 rather than a 401. The 401 writer sets WWW-Authenticate: Bearer. The deliberate asymmetry — an absent header is anonymous, not unauthorized — is what lets the permission gate produce the 401 with its own message; the gate's own two statements are catalogued separately as R-IDENTITY-016 (unactivated account) and R-IDENTITY-017 (missing code) because their refusal shapes differ. VERIFY GAP: no unit test for the middleware (only ratelimiter_test.go exists in kernel:httpx); the whole ladder is pinned by one DB-backed e2e suite.
