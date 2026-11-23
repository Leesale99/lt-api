---
id: R-GAME-023
title: The two settlements of one round run back to back
statement: |
  A match settlement and a round settlement of the same round never run beside
  each other: each takes the round's settlement lock before reading any ride,
  so the second one sees the first one's committed transitions instead of
  racing them. Without that order the later settlement would lose its write,
  roll back — its event claim with it — and the event would be dropped.
module: game
entity: ride
category: orchestration
status: implemented
authority: repository
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/adapter/outbound/postgres/transactor.go::Transactor.LockSettlementRound
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.runEvent
    layer: service
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
stale_pointer: null
tie: |-
  dropping the call lets the two handlers race, and the lost CAS strands a ride, which is what TestSettlementHandlersSerializeOnTheRound reproduces (the delivery must wait, not fail).
---

## Notes

Authority is the repository (the ordering/locking primitive is the transactor's, exactly the taxonomy's "ordering/locking/atomicity mechanics → repository"), a deviation from the orchestration default (service) recorded here: the use case decides *that* the lock is taken and *when* in its sequence (before any ride read), the adapter decides *how* (a transaction-scoped advisory lock, one key namespace plus the round id). The service site therefore is a copy that fails loudly — No refusal: the rule's failure mode is a stranded ride and a rolled-back claim, never a client-facing error.
