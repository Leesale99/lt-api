---
id: R-GAME-007
title: Each ride command opens only in its phase window
statement: |
  Creating a ride is allowed only before the round's first match; recording a win or a
  loss only while the round's matches are running; continuing, burning or unlocking
  only after the last match of the round has ended. A command issued in the wrong
  window is refused and changes nothing.
module: game
entity: ride
category: policy
status: implemented
authority: domain
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/domain/ride.go::Ride.Create
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.WonPending
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Lost
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Lock
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Burn
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::Ride.Unlock
    layer: domain
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidRoundPhase
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_test.go::TestRide_Create
    - internal/game/internal/core/domain/ride_test.go::TestRide_WonPending
    - internal/game/internal/core/domain/ride_test.go::TestRide_Lost
    - internal/game/internal/core/domain/ride_test.go::TestRide_Lock
    - internal/game/internal/core/domain/ride_test.go::TestRide_Burn
    - internal/game/internal/core/domain/ride_test.go::TestRide_Unlock
    - internal/game/internal/core/service/service_test.go::TestService_RideCommandPhaseRejection
  e2e:
    - internal/e2e/rides_test.go::TestRideCommandHandlers
  gherkin:
    - docs/spec/features/game/ride_lifecycle.feature::A lock attempt inside the last hour before kickoff is refused
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Six authoritative sites on purpose: the window is per command, and each command states its own (they are disjoint cases, not copies of one statement). The `transitions` map supplies the state half of the guard; the phase constant each guard compares against supplies the window. The phase *value* is the calendar's (schedule derives it from real-world match times through the anti-corruption layer); that derivation is a schedule rule, not this one. The refusal is Go-only (ErrInvalidRoundPhase, 409) precisely so it stays distinguishable from a state refusal — the two are different client-facing errors.
