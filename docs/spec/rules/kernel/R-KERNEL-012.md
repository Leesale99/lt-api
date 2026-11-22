---
id: R-KERNEL-012
title: A request body is read under a one-megabyte cap
statement: |
  No request body is read beyond one megabyte. A body over that limit is refused as too
  large before anything is decoded or deduplicated, so an oversized request claims no
  idempotency key and a retry with a normal body executes normally.
module: kernel
entity: http_request
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/json.go::MaxBodyBytes
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/json.go::ReadJSON
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken
    layer: handler
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 413
fixture: null
verification:
  unit: null
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the two wraps share httpx.MaxBodyBytes, so the limits cannot diverge; the 413 writer names the limit it enforced.
---

## Notes

One constant, two intake wraps, and they must agree: ReadJSON caps at decode, and the body-carrying idempotent command caps before it drains the body for the digest — capping only at decode would buffer an oversize body in full, store a digest of bytes the retry can never re-read, and turn an identical retry into a 409 (the comment at the site spells that failure out; the drain-then-restore is the reason the rule cannot live in ReadJSON alone). Renumbered at merge (was provisional R-KERNEL-013): R-KERNEL-009 was folded into R-IDENTITY-016/R-IDENTITY-017. Related but distinct: R-PLATFORM-005 parses the deployment's allowlist at startup, this rule matches a request's Origin against it — different authorities (config parsing vs request framing), so two rules.
