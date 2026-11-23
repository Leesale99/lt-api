---
id: R-GAME-001
title: Rides are born locked
statement: |
  A ride may only come into existence in the locked state. Every other state is
  reachable only through a transition the state gate checks, so no writer — the
  application, a migration script, a test shortcut — can plant a ride directly in
  won_pending, burned, unlocked or lost.
module: game
entity: ride
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: row-local
enforcement:
  - site: migrations/000012_add_rides_insert_state_gate.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Create
    layer: domain
    role: defense-in-depth
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - ride_insert_requires_locked_state
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_test.go::TestRide_Create
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_InsertAndGet
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandler
  gherkin:
    - docs/spec/features/game/ride_lifecycle.feature::A new ride is born locked
expert-ref: docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: |-
  Ride.Create fails loudly — it never drifts silently, because a wrong copy makes 000012 refuse the INSERT (409) and TestRide_Create fails first.
---

## Notes

The insert half of the gate is a trigger, not a CHECK: the predicate is INSERT-only ("state = 'locked'"), and a CHECK would also refuse the UPDATEs that reach the other four states. The Go site is not a second check — Ride.Create *constructs* the initial state (overwriting whatever the caller supplied, acc and streak included). No shared fixture exists between the two sites, hence pinned_by: null.
