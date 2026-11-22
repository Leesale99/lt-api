---
id: R-GAME-002
title: Ride state transitions follow the five-state graph
statement: |
  A ride moves only along the legal graph: locked → won_pending or lost;
  won_pending → locked (continuation), burned or unlocked. No other state change is
  possible for any writer.
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
  - site: internal/game/internal/core/domain/ride.go::transitions
    layer: domain
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::canTransition
    layer: domain
    role: defense-in-depth
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - ride_state_transition_illegal
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_test.go::TestRide_InvalidTransitions
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx
  e2e:
    - internal/e2e/rides_test.go::TestRideCommandHandlers
  gherkin:
    - docs/spec/features/game/ride_lifecycle.feature::A won match leaves the ride waiting on a decision
expert-ref: docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: |-
  both Go sites fail loudly — they never drift silently, because the trigger refuses the arrival and the refused ride's own test fails first.
---

## Notes

The graph is OLD-vs-NEW, so it must live in the trigger (check 3 of rides_block_illegal_transition, unchanged since 000006, re-created verbatim by 000017 as game.rides_block_illegal_transition). The domain's `transitions` map is the same graph in Go, used by every command guard; it is a copy that fails loud (the trigger refuses the write, TestRide_InvalidTransitions fails first). `canTransition` is the lookup, listed separately because a change to the map alone would not show up in a diff of the command methods. The store's guarded writes are the two transactional production paths (DecisionGuardedTx for decisions, ArriveTx for settlement arrivals); the test-only RideRepository.Update that used to stand in for them was removed in Phase G, and its cases moved onto those paths (misalignment-report §6 item 21).
