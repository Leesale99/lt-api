---
id: R-GAME-024
title: A refused ride write names its cause in a fixed order
statement: |
  When a ride write is refused, the reason is classified in a fixed order —
  the row is gone, the state or version the command claimed no longer holds,
  then the database's own refusal — so the client always gets the refusal that
  actually happened. A write that matched nothing while every predicate still
  holds is reported as a defect, never dressed up as a business refusal.
module: game
entity: ride
category: refusal-mapping
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyDecisionRefusal
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyArrivalRefusal
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.DecisionGuardedTx
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
    - internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideCommandsRaceOnOneRide
    - internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideBurnRacesExactlyOnce
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  two comments in the ride adapter still name the old shape — DecisionGuardedTx's doc comment says a destination deleted between the port gate and the write "surfaces as the FK 23503 -> ErrNotFound", while the inline comment two screens down says (correctly) that since 000016 no FK is left on rides and the branch is only the generic race-mapping shape; ride_repo_test.go's comments still cite classifyUpdateRefusal, a symbol that no longer exists (the classifiers are classifyDecisionRefusal, classifyArrivalRefusal and classifyResolveRefusal).
tie: null
---

## Notes

This is the catalog's first refusal-mapping rule (the category the schema reserves for rules *about* the mapping). The order is: the statement's own SQLSTATEs first (a trigger refusal and the money-state check both mean the state machine refused → 409; a foreign-key refusal is the parent gone → 404), then, for a zero-row write, a follow-up read on the pool: no row → not found, row moved (version, or state no longer the claimed pre-state) → invalid transition, otherwise a wrapped error with no sentinel → 500. Two authoritative classifier sites because the two guarded writes have different pre-states (a decision claims won_pending; an arrival claims its own from-state, which may be locked *or* won_pending) — disjoint cases of one statement. The design point worth preserving: the follow-up read may legally see a snapshot from before the concurrent commit, so the "everything still holds" branch must not be folded into a named refusal — that is the loud error. The round's facts are deliberately absent from this classification: a close that slipped in between the gate and the write is the round refusal, decided before the write (R-GAME-008). Renumbered at merge (was provisional R-GAME-025): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely.
