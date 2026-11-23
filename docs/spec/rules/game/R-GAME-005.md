---
id: R-GAME-005
title: Unlocked and lost rides carry no accrued bonus
statement: |
  An unlocked or lost ride forfeits everything it accrued: its accumulated bonus is
  zero at the moment it reaches that state. A burned ride keeps the bonus it accrued,
  because the tokens and the bonus are fulfilled as TB.
module: game
entity: ride
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000013_add_rides_money_state_coupling.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/ledger/postgres.go::Repository.ReleaseBonus
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Unlock
    layer: domain
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Lost
    layer: domain
    role: defense-in-depth
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - rides_money_state_coupling
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/constraints_test.go::TestRideMoneyStateCoupling
    - internal/game/internal/core/domain/ride_test.go::TestRide_Unlock
    - internal/game/internal/core/domain/ride_test.go::TestRide_Lost
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx
  e2e: null
  gherkin:
    - docs/spec/features/game/settlement.feature::Unlocking a won ride forfeits the bonus it accrued
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: |-
  the rule holds — the CHECK is enforced, mapped by constraint name to ErrInvalidTransition (409), and probed by TestRideMoneyStateCoupling — but migration 000013's change-detector prose no longer describes the code. It names the domain copies as "(internal/game/rides.go)", the persistence copy as "rideAutoUnlockSQL's bonus_acc = 0 (internal/game/rounds.go)" and the DB probe as "(internal/game/constraints_test.go)". None of those paths exist after the modular-monolith refactor: the domain operations are in internal/game/internal/core/domain/ride.go, the copy that actually reaches the row is internal/ledger/postgres.go::Repository.ReleaseBonus (the repository's UPDATE deliberately omits every money column), and the probe is internal/game/internal/core/service/constraints_test.go. The rule's content is unchanged, so the status stays `implemented` — rot is not drift; the applied migration is immutable, so the fix is a comment at the current sites, not a migration rewrite. Second finding: the domain copies zero an in-memory Acc that the repository never persists, so they are documentation of intent — the load-bearing copies are the CHECK and the ledger's release-before-flip order.
tie: |-
  the ReleaseBonus and domain copies fail loudly — they never drift silently, because the CHECK refuses an unlocked/lost row carrying acc (23514 → 409) and TestRideMoneyStateCoupling pins the refusal name.
---

## Notes

A shared fixture (moneyStateRule: unlocked/lost/burned × acc 0/100, run through the ledger seam and direct SQL) would tie them without relying on the refusal.
