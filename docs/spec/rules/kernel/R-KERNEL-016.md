---
id: R-KERNEL-016
title: A relay delivers committed events and marks them published
statement: |
  Events owed by committed transactions are read from the outbox by a relay that runs
  outside the request, delivered to their consumers, and marked published only after the
  delivery, so a crash leaves the event to be delivered again.
module: kernel
entity: outbox_event
category: orchestration
status: specified-only
authority: service
mechanism: null
write_scope: null
enforcement: []
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 08 §"Dispatcher" (start with one in-process dispatcher) and the orientation sequence diagram: read unpublished events -> deliver -> mark published; the minimal schema's nullable published_at is the marker; tasks 3 and 7 add the dispatcher and restart/retry tests. The bus pushes synchronously from the publishing request and carries no persistence, retry or dispatch loop — its own package comment says "There is no persistence, retry or at-least-once guarantee". The outbox_events table (id, type, aggregate_id, payload, occurred_at, published_at) does not exist, so nothing can be read or marked.
