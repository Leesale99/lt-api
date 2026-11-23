---
id: R-GAME-010
title: A settled ride agrees with the committed match result
statement: |
  A locked ride may only arrive at won_pending when the committed result of its match
  crowns the ride's team, and at lost when it does not; a draw crowns nobody. A
  settlement payload that claims the wrong destination is refused and the whole
  settlement transaction rolls back.
module: game
entity: ride
category: invariant
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/game/internal/core/service/settlement.go::resultAgreement
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.settleRideFromResult
    layer: service
    role: defense-in-depth
    pinned_by: scoreStateRule
  - site: internal/schedule/internal/model/match.go::Match.Winner
    layer: domain
    role: defense-in-depth
    pinned_by: scoreStateRule
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidTransition
  http: 409
fixture: scoreStateRule
verification:
  unit:
    - internal/game/internal/core/service/score_state_rule_test.go::TestWinnerScoreStateRule
    - internal/game/internal/core/service/score_state_rule_test.go::TestScoreStateRuleDB
    - internal/schedule/internal/model/matches_winner_test.go::TestWinner
    - internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_ArriveTx
  e2e: null
  gherkin:
    - docs/spec/features/game/settlement.feature::Each ride arrives where the committed result puts it
    - docs/spec/features/game/settlement.feature::A settlement that misreports the odds is refused and pays nothing
expert-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the service, deviating from the invariant default on purpose: 000017 moved the check out of rides_state_gate because the trigger read schedule's matches table — the cross-context read the boundary rule removes — so the same refusal now happens in Go inside the same transaction, before the arrival write. The two historical refusal names survive as text in the Go errors (ride_result_scores_missing / ride_result_disagreement) with no SQLSTATE. The committed snapshot the agreement re-reads also carries the odds: the payload's rate claim is verified against it (refusal text ride_result_odds_disagreement, the same ErrInvalidTransition + rollback as the other two refusals), and the payout rate is read from the committed row with the side picked by the committed team assignment (a side-swapping payload that passed a bare pair-compare could not misprice a won ride). The loud refusal is this rule's claim-refusal pattern applied to the rate, and R-GAME-026's "unverified"/"loud error" language is the same choice.
