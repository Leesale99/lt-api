---
id: R-KERNEL-011
title: A panic is answered as a server error, never explained
statement: |
  A failure inside the request chain is answered with the generic server-error message
  and the connection is closed; the cause is recorded in the log and never returned to
  the caller. No internal detail — a stack, a driver message, a query — reaches a
  client through this path.
module: kernel
entity: http_request
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/middleware.go::Middleware.RecoverPanic
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/errors.go::ServerError
    layer: handler
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 500
fixture: null
verification:
  unit:
    - internal/kernel/httpx/middleware_test.go::TestRecoverPanicAnswersServerError
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

RecoverPanic sits outermost in the chain (R-PLATFORM-014), so it also catches failures raised by the other middleware, and it sets Connection: close before writing. ServerError is the single 500 writer for every non-domain failure path (middleware lookups, unmapped repository errors), which is why an unmapped 23514/42501 surfaces as the same generic message. A middleware probe now pins it: a panic raised in the chain is answered 500 with the generic message and Connection: close, and the body carries no panic value or stack. Renumbered at merge (was provisional R-KERNEL-012): R-KERNEL-009 was folded into R-IDENTITY-016/R-IDENTITY-017.
