---
id: R-GAME-016
title: A settlement reaches each ride once
statement: |
  A settlement arrival only lands on a ride that is still in the state the
  handler read and at the version it read; a ride another writer already moved
  is left alone and the arrival reports the invalid-transition conflict. A
  settlement therefore never overwrites a decision, a replay or a sibling
  settlement.
module: game
entity: ride
category: orchestration
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ArriveTx
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyArrivalRefusal
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.LockedForMatchTx
    layer: repository
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound
    - internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded
    - internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce
  gherkin:
    - docs/spec/features/game/settlement.feature::The same result arriving twice pays the bonus once
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: null
tie: null
---

## Notes

Authority is the repository, which deviates from the orchestration default (service) on purpose: the use case decides *which* rides are candidates, but the guarantee that only a still-arrivable ride is written is a conditional UPDATE whose WHERE carries the from-state and the version — the check and the write are one statement under READ COMMITTED, so no use-case code can be the authority for it. classifyArrivalRefusal is listed as authoritative too because the refusal's *name* is decided there (row gone → not found, version moved → invalid transition) and the seed's refusal surface is the 409. The trigger path is the other half of the same answer: a P0001 from the state graph also maps to the invalid-transition conflict. LockedForMatchTx is listed because the candidate set is part of the statement — only rides still named locked are settled, so a won_pending ride is never settled twice. Deliberately not part of this rule: what the destination must be (R-GAME-010), the event claim (R-KERNEL-014) and the per-round serialization (R-GAME-023). Verification gap: no unit test calls ArriveTx with a stale pre-state directly; the CAS is pinned through the settlement races and the store's other guarded write (TestRideRepository_DecisionGuardedTx), which applies the same from-state + version predicate. The one ArriveTx case that is unit-pinned is the store boundary itself (TestRideRepository_ArriveTx): the write lands without a result-agreement check, because that refusal is the service's (R-GAME-010). Related but distinct: R-KERNEL-014 claims the *event* at the service, this rule claims the *ride row* at the repository — the authorities differ, so they are two rules (a replay can be dropped by the claim and still be settled idempotently per ride, and vice versa).
