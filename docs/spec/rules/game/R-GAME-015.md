---
id: R-GAME-015
title: A forfeit is posted before the ride's state moves
statement: |
  Whenever a ride forfeits its accrued bonus — a player unlock, a lost result,
  the season-close force-unlock — the money is released first and the state
  changes after, both inside one transaction. The order is what makes the
  arrival legal: a ride cannot reach unlocked or lost still carrying a bonus.
module: game
entity: ride
category: orchestration
status: drifted
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideUnlock
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.settleRideFromResult
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted
    layer: service
    role: authoritative
    pinned_by: null
  - site: migrations/000013_add_rides_money_state_coupling.up.sql
    layer: db-constraint
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
    - internal/game/internal/core/service/service_test.go::TestService_RideUnlock
    - internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded
    - internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted
    - internal/game/internal/core/service/constraints_test.go::TestRideMoneyStateCoupling
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md
stale_pointer: null
tie: |-
  if the flip were written before the release, 000013 refuses the UPDATE with 23514 and the whole transaction (release included) rolls back, so the drift cannot land silently.
---

## Notes

Ordering is a use-case decision (which statement runs first, and that both share the command's transaction), so the authority is the service; the money write itself belongs to the ledger behind its intent seam, and the ride repository deliberately omits every money column from its SET clause. Three authoritative sites are the three roads into a forfeiting arrival (player unlock, the lost half of a match settlement, the round-close sweep) — one statement, disjoint cases. The constraint site is a data-carrying copy that fails loudly, hence defense-in-depth with no fixture: Related but distinct: R-GAME-005 owns the coupling predicate itself, this rule owns the order in which the two writes are issued. DRIFT. The statement names the season-close sweep as a third forfeit, "the season-close force-unlock", and has it release the accrued bonus first. The domain sources end the season with a burn, not an unlock: domain-knowledge.md §10 Season End and Playoffs, "Season-end auto-burn", burns every pending winning ride and turns "Locked Tokens and accumulated acc" into TB, and domain-knowledge-game-engine.md §5.6 "Burn a Ride" keeps acc inside the TB credit (tb_credit = tokens_locked + acc); neither source has a season-close unlock. The catalog believes the sources under the source-wins invariant, so this framing is drift: when the season closes the bonus is kept, not released. The code implements the rule's wording for every non-final close (Service.OnRoundCompleted releases the bonus, then flips the ride to unlocked).
