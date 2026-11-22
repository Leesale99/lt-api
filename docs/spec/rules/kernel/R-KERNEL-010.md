---
id: R-KERNEL-010
title: Cross-origin access is an exact allowlist, silently withheld otherwise
statement: |
  Only origins named in the deployment's allowlist are granted cross-origin access, by
  exact match. An unlisted origin receives no cross-origin permission at all — the
  request still completes, and the browser withholds the response — and a preflight
  from an allowed origin is answered immediately, before any throttle token is spent
  and before any credential is read.
module: kernel
entity: cors_origin
category: policy
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/middleware.go::Middleware.EnableCORS
    layer: handler
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: null
fixture: null
verification:
  unit: null
  e2e:
    - internal/e2e/cors_test.go::TestEnableCORS
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (policy → domain): CORS is browser-enforced framing, decided entirely by the transport. The allowlist arrives from the deployment (platform's parseCORSOrigins, R-PLATFORM-005, which refuses a wildcard at boot); the middleware only matches, echoes the matched origin, adds Vary: Origin, and advertises PUT/PATCH/DELETE plus the Authorization and Content-Type request headers with a one-day preflight cache. `refusal.http` is null because the server never refuses: an empty TrustedOrigins list means "not allowed", never "allow all". The silent-withholding choice is a deliberate trade (curl and server-to-server calls send no Origin), recorded in the code; it means a misconfigured allowlist is invisible server-side. Renumbered at merge (was provisional R-KERNEL-011): R-KERNEL-009 was folded into R-IDENTITY-016/R-IDENTITY-017.
