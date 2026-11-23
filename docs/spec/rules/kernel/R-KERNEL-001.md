---
id: R-KERNEL-001
title: An idempotent command must carry a usable idempotency key
statement: |
  Every command that promises retry safety must arrive with a key that names one
  logical request: a non-empty value of at most 255 bytes. A request without one is
  refused as a malformed request, and no writer — a script, a future service — can
  store a key outside those bounds.
module: kernel
entity: idempotency_key
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/kernel/idempotency/idempotency.go::ValidateIdempotencyToken
    layer: validator
    role: authoritative
    pinned_by: null
  - site: migrations/000010_add_idempotency_keys_table.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint:
    - idempotency_keys_key_check
  sentinel: null
  http: 422
fixture: null
verification:
  unit:
    - internal/kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandler
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: null
tie: |-
  a wrong copy fails loudly — a key the validator accepts but the table refuses makes the command's transaction fail (500), and the CHECK is the only half a non-Go writer meets.
---

## Notes

The validator is the authority (it produces the field error and the 422); the table's CHECK is the path-independent backstop, carrying the same 255-byte bound. The namespace and the lifetime are not this rule's: since migration 000022 the claim row is (key, user_id), so keys are scoped to their caller (and 000010's rows carried over as system claims, user_id 0), and the kernel's claim sweeps rows older than a day. R-KERNEL-003 owns both facts.
