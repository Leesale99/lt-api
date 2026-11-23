---
id: R-GAME-003
title: Terminal rides are frozen
statement: |
  A burned, lost or unlocked ride is final: its state never changes again and it is
  never re-pointed at another match. A state-preserving rewrite of the row is not a
  transition and is allowed.
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
  - site: internal/game/internal/core/domain/ride.go::canTransition
    layer: domain
    role: defense-in-depth
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - ride_terminal_state_frozen
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_test.go::TestRide_TerminalStates
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_TerminalRideGate
  e2e: null
  gherkin:
    - docs/spec/features/game/ride_lifecycle.feature::A burned ride accepts no further command
expert-ref: docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: |-
  the domain site fails loudly — it never drifts silently, because a terminal ride that accepted a transition would be refused by the trigger and TestRide_TerminalStates fails first.
---

## Notes

Trigger check 1. The domain copy is the absence of any entry for a terminal state in `transitions` (canTransition returns false), so the refusal surfaces as ErrInvalidTransition from the command, and the repository's UPDATE would be refused with the same 409. The same check also freezes match_id on terminal rides, which is why the "state-preserving full-row rewrite" carve-out is part of the rule.
