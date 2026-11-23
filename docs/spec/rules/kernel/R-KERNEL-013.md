---
id: R-KERNEL-013
title: An event is a post-commit, same-process notification
statement: |
  A domain event is delivered only after the publishing operation has committed, to
  subscribers inside the same process, in the order they subscribed. Nothing is stored
  and nothing is retried: a subscriber that fails, or a process that dies between the
  commit and the delivery, drops that event — the publisher's committed write stands
  and must not be rolled back.
module: kernel
entity: domain_event
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/eventbus/eventbus.go::Bus.Publish
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/kernel/eventbus/eventbus.go::Bus.Subscribe
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/service/service.go::publish
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/service/round_lifecycle.go::RoundService.Close
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/kernel/eventbus/eventbus_test.go::TestBus_SubscribeAndPublish
    - internal/kernel/eventbus/eventbus_test.go::TestBus_PublishReturnsFirstHandlerError
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Four disjoint cases of one contract: the bus's delivery mechanics (synchronous, registration order, first handler error returned, no persistence) and each publisher's post-commit ordering (resolve and close both publish after Commit; the shared publish helper drops its context cancellation so a gone request cannot cut settlement short, and logs a failure as the accepted gap). Layer note: the kernel bus is infrastructure and the layer enum has no infrastructure value (D3 froze seven), so it is recorded as service — the layer that owns ordering. Deliberately NOT catalogued as implemented: the transactional outbox and at-least-once delivery that phase 08 specifies (outbox_events table, dispatcher, published_at) do not exist; the guarantee is strictly weaker (at-most-once, in memory) — a gap the phase-08 guide (docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md) owns, not this rule. No refusal: an event is a notification — nothing is refused when a delivery is dropped, which is precisely the gap (the refusal a dropped event would need does not exist; it is R-KERNEL-015..017's subject).
