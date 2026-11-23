---
id: R-KERNEL-014
title: A subscriber claims the event before it acts
statement: |
  A subscriber records the event's identity before it writes anything, so an event
  delivered twice is handled once: the second delivery finds the claim and completes
  as a no-op. A subscriber that fails leaves no claim behind, so the same event may be
  delivered and processed again.
module: kernel
entity: domain_event
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/settlement.go::Service.runEvent
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.OnMatchResultRecorded
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/kernel/idempotency/idempotency.go::Repository.Claim
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded
    - internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md
knowledge-ref: null
stale_pointer: null
tie: |-
  the kernel claim is the same table the HTTP commands use, so it cannot drift from this rule without breaking the whole idempotency engine; and if the claim were dropped, a replay's arrivals would be refused by the state-and-version guard (P0001 → invalid transition), so the mistake is loud rather than double-applied.
---

## Notes

The event's ID is the idempotency key and a digest of the payload is its request hash, so redelivery of the same event replays (no-op success) while a different payload under the same ID is the kernel's conflict — the subscriber inherits R-KERNEL-004's claim-first shape rather than inventing one. This is what makes R-KERNEL-013's drop-on-failure tolerable: the platform may re-deliver freely, and the transactional outbox can replay without the subscriber changing. Recorded gap: the two handlers live in one context — a future subscriber elsewhere must adopt the same shape by hand; nothing in the kernel enforces that, and no test pins a foreign subscriber. Merged (dupe policy): the settlement event's claim is this rule's game instantiation, not a rule of its own — same `runEvent` site, same two handlers, same tests. The game call-sites stay listed here as enforcement sites of this one authority: `runEvent` is authoritative for the decision, the two handlers are authoritative for the same reason (both share the shape — the claim lives in `runEvent` and each handler only supplies its endpoint, payload and work, so the three are disjoint cases of one statement, not copies), and migration 000010's claim table is the shared mechanism the HTTP commands use too. The in-transaction claim is also what makes a *failed* settlement retryable: the claim rolls back with the work, which is the opposite of caching a failure. No refusal of its own: a replay is a success by design (at-least-once delivery with an idempotent consumer; the catalog never claims "exactly once" delivery), so there is no client-facing refusal here.
