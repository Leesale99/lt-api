---
id: R-KERNEL-015
title: An event is committed with the change that caused it
statement: |
  The fact that an event must be published is committed in the same transaction as the
  state change itself: if the change commits the event is owed, and if the change rolls
  back the event was never owed.
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

Specified only. Phase 08 §"New concept" states the guarantee verbatim ("The state change and the fact that the event should be published are committed together"), and §"Example transaction" shows the INSERT INTO outbox_events inside the caller's tx; tasks 1-2 are the table and the same-transaction write. What exists is the opposite guarantee: delivery is post-commit, in-process and synchronous (internal/kernel/eventbus/eventbus.go; internal/schedule/internal/service/service.go ::publish on context.WithoutCancel), so a crash between commit and publish drops the event — documented as the accepted gap in both files. No outbox_events table exists (grep of migrations/ finds only comments pointing at this phase) and no transaction contains an append step. Authority is the service: the use case owns the transaction that must contain both writes.
