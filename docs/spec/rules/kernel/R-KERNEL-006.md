---
id: R-KERNEL-006
title: Request validation is collected, not fail-fast
statement: |
  A malformed request reports every offending field at once, one message per field, so
  the caller can fix the whole request in one round trip. A request that fails no
  field check is the only one that reaches the operation behind the handler.
module: kernel
entity: http_request
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/validator/validator.go::Validator.AddError
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/kernel/validator/validator.go::Validator.Check
    layer: validator
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 422
fixture: null
verification:
  unit: null
  e2e:
    - internal/e2e/teams_test.go::TestCreateTeamHandler
    - internal/e2e/rides_test.go::TestCreateRideHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The kernel is the *collection* policy, not any particular check: a field keeps its first message (AddError is first-wins), Valid() is the single gate, and the handler answers one 422 carrying the whole map through httpx.FailedValidation (the renderer, not a second decision point). Every concrete rule (which fields, which bounds) belongs to the module that owns the request and is catalogued there. VERIFY GAP: the kernel package has no unit test of its own (no validator_test.go) — the first-wins behaviour and the empty-map gate are pinned only indirectly through handler-level 422 assertions, and "one message per field" is never asserted.
