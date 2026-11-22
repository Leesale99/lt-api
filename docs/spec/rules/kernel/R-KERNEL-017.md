---
id: R-KERNEL-017
title: Delivery is at-least-once and never claimed exactly-once
statement: |
  An event a transaction committed to publish is eventually delivered at least once, and
  the system makes no exactly-once claim: a consumer must tolerate the same event
  arriving twice.
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

Specified only for the delivery half. Phase 08 §"Dispatcher": "A consumer must be idempotent. Do not claim 'exactly once'. Aim for: at-least-once delivery + idempotent consumers." The idempotent-consumer half IS implemented and belongs to the code sweep: both settlement handlers claim the EventID through kernel/idempotency before writing (internal/game/internal/core/service/settlement.go::runEvent — "claim the EventID first ... a replay is a no-op success"), and test coverage exists in service/idempotency_test.go. The delivery half is at-most-once today: a pubsub drop is gone forever (no persistence, no retry, no redelivery), so "eventually delivered" cannot hold. Numbered from the phase-doc sweep (was provisional R-KERNEL-203).
