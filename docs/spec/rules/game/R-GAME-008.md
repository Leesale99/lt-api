---
id: R-GAME-008
title: A ride write requires an open round at the write instant
statement: |
  A ride command may only write while the round it touches is open, and that fact is
  pinned at the moment of the write: the calendar's share-locked guard runs inside the
  command's transaction, so a concurrent round close either commits before the command
  reads (and the command is refused) or waits for it.
module: game
entity: ride
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
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
  - site: internal/game/internal/adapter/outbound/schedule_acl.go::ScheduleACL.RoundOpenTx
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: migrations/000018_context_roles.up.sql
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrRoundNotOpen
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideLockRacesRoundClose
    - internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideCreateRacesRoundClose
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandler
  gherkin:
    - docs/spec/features/game/settlement.feature::A command after the round has closed is refused and writes nothing
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the two repository sites fail loudly — they never drift silently, because the command cannot commit an ungated write without losing the serialization the race tests (TestService_RideCreateRacesRoundClose / RideLockRacesRoundClose) assert.
---

## Notes

The decision is the service's (every command must run the gate inside its own unit of work, before its write); the *guarantee* is the FOR SHARE row lock in schedule's own SECURITY DEFINER functions (round_open_guard / destination_open_guard, 000018), which is why the guard is listed as defense-in-depth at the repository layer: Postgres executes it, but the ordering decision — gate before write, same transaction — is the use case's. The advisory phase read (PhaseForMatch) proves nothing and is not listed. The one silent failure mode is structural: a NEW ride command that forgets to call the gate writes without the lock, and no fixture pins the call set.
