---
id: R-KERNEL-004
title: A claim commits with its command, never before or after it
statement: |
  The claim on a key and the stored answer are written inside the command's own
  transaction, so a command that fails leaves no claim behind (failures are never
  cached) and a command that succeeds commits its claim together with its effect. A
  concurrent duplicate waits on the claim and then receives the committed answer
  instead of executing a second time.
module: kernel
entity: idempotency_key
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideCreate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideLock
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideBurn
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideUnlock
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/kernel/idempotency/idempotency.go::Repository.Claim
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/kernel/idempotency/idempotency.go::Repository.Save
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideIdempotentHandler
    - internal/e2e/rides_test.go::TestRideLockIdempotentHandler
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: null
tie: |-
  the kernel half cannot reorder anything — Claim and Save both take a caller-owned transaction and never commit, so the ordering decision cannot hide in the kernel, and a command that executed before claiming would find no key row and fail its own replay test first.
---

## Notes

Four authoritative sites are the four ride commands (disjoint cases, one per command — D4's granularity rule); the settlement handlers use the same shape through Service.runEvent, catalogued as R-KERNEL-014 because its subject is event redelivery. Single-flight is by insert conflict (ON CONFLICT DO NOTHING), not by an in-flight state machine: there is no "in progress" state to time out and no lock to leak. Recorded gap: a claim left behind by a process crash mid-command is impossible (the transaction rolls back), but a claimed row with the placeholder status/body of an uncommitted command can only exist inside that command's transaction, so no reader ever sees it. No refusal of its own: the ordering is a property of where the claim is written, not a predicate that can refuse — a broken ordering shows up as a lost replay or a rolled-back claim, never as a status code of its own.
