---
id: R-PLATFORM-014
title: The request chain order is fixed
statement: |
  Every request traverses the same chain in the same order: panic recovery outermost,
  then cross-origin handling, then throttling, then credential resolution, then the
  route. That order is what guarantees a panic is always answered, a preflight never
  spends a throttle token and never needs a credential, and a caller is throttled
  before any work is done on its behalf.
module: platform
entity: http_request
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/httpchain/httpchain.go::Build
    layer: composition-root
    role: authoritative
    pinned_by: null
  - site: cmd/api/routes.go::routes
    layer: handler
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/httpchain/httpchain_test.go::TestBuildOrder
  e2e:
    - internal/e2e/cors_test.go::TestEnableCORS
    - internal/e2e/auth_test.go::TestAuthenticateMiddleware
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (orchestration → service): the chain is assembled once, at the composition root, over modules that register their own routes behind a gate they are handed — no module can reorder the chain, and the router's own 404/405 writers sit inside it. The nesting RecoverPanic(EnableCORS(RateLimit(Authenticate(router)))) lives in ONE builder, internal/platform/httpchain.Build, and both call sites ask it: cmd/api/routes.go and the end-to-end harness (internal/e2e/compose_test.go::routes) hand it their own router and Middleware — the suite keeps its substitutions (its origin allowlist, its limiter) without owning the order. The fidelity gap the report recorded (§6 item 19: the harness rebuilt the chain independently, so a reordering in one place left the suite green) is closed by that single site; TestBuildOrder then pins the order itself, one case per adjacent pair — a panicking route is answered (recovery outermost), an allowed preflight spends no throttle token (CORS outside throttling), an empty bucket answers 429 to a request with a bad credential (throttling outside authentication), and a bad credential is refused on an unserved path (authentication outside the router). The CORS suite asserts the preflight-before-router short-circuit from the other end.
