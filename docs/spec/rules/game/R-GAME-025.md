---
id: R-GAME-025
title: A ride is re-pointed at another match only by continuing it
statement: |
  A ride may change the match it is on only when a won ride continues onto a
  later match; no other write may move it, so a ride can never be silently
  transplanted onto somebody else's fixture. A state-preserving rewrite keeps
  the same match and is allowed.
module: game
entity: ride
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: row-local
enforcement:
  - site: migrations/000017_app_level_result_agreement.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Lock
    layer: domain
    role: defense-in-depth
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - ride_match_id_immutable_outside_continuation
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx
    - internal/game/internal/core/domain/ride_test.go::TestRide_Lock
  e2e: null
  gherkin:
    - docs/spec/features/game/ride_lifecycle.feature::Continuing moves the won ride onto its next match
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md
stale_pointer: null
tie: |-
  if the domain let another command move the match, the trigger refuses the UPDATE (P0001 → 409) and TestRideRepository_DecisionGuardedTx's re-point case fails first.
---

## Notes

Trigger check 2 of the same function that carries R-GAME-002 (the state graph) and R-GAME-003 (terminal freeze), but a separate rule: the predicate is about the match column, not about the state, and it is falsifiable on its own (a locked ride re-pointed at another match leaves the state graph untouched and is still refused). The domain site is the only operation that writes the match at all, so it is a copy that fails loudly — On terminal rows this check and R-GAME-003's freeze both fire; the freeze name is the one that wins there because it is evaluated first, which is why the continuation carve-out is stated in both rules' statements rather than treated as a contradiction.
