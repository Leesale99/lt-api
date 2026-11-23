---
id: R-GAME-009
title: Round completion force-resolves undecided rides
statement: |
  When a round completes, every ride still waiting on a decision is resolved by the
  system: on rounds one to thirty-seven it is unlocked and the accrued bonus is
  released; on the season's final round it is burned and the accrued bonus is kept,
  because a chase option no longer exists. Still-locked rides of the round's matches
  are settled first, and all of it happens in the one claimed settlement transaction.
module: game
entity: ride
category: policy
status: drifted
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/settlement.go::Service.runEvent
    layer: service
    role: defense-in-depth
    pinned_by: null
  - site: internal/schedule/internal/service/round_lifecycle.go::RoundService.Close
    layer: service
    role: authoritative
    pinned_by: null
refusal:
  code: P0001
  constraint: null
  sentinel: ErrInvalidTransition
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted
    - internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound
  e2e: null
  gherkin:
    - docs/spec/features/game/settlement.feature::A completing round unlocks the ride still waiting on a decision
expert-ref: docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md
knowledge-ref: null
stale_pointer: null
tie: |-
  runEvent fails loudly — it never drifts silently, because a dropped claim lets a replay re-run the arrivals, which the state gate then refuses (P0001 → 409).
---

## Notes

Authority is the service, not the domain, and that is a deliberate deviation from the policy default: settlement is system-initiated, so the domain commands' DecisionPhase guards (R-GAME-007) do not apply — the settlement writes go through the same DB transition graph as every other writer, via guarded arrivals. The final-round fact is the publisher's (RoundService.Close derives IsFinal from closed.Number == model.RoundsPerSeason) and reaches game only in the payload, which is why two contexts' services appear as authoritative sites: each owns one half of the fact across the event seam. runEvent is listed because the ordering is load-bearing — claim the event id, take the per-round settlement lock, then read rides: without it two settlement handlers of one round race and the second reads a stale snapshot (TestSettlementHandlersSerializeOnTheRound). Refusal surface: the force-resolve arrivals can only fail on the state gate (P0001 → ErrInvalidTransition, 409). The accepted crash gap (close committed, settlement not yet run) is deliberately not a rule — nothing in the code closes it. DRIFT. The statement inverts the domain's rule: it unlocks the undecided ride "on rounds one to thirty-seven" and burns it only "on the season's final round". The domain sources burn it as soon as the team's Action Phase closes — domain-knowledge.md §7 Ride Rules, auto-burn subsection, and domain-knowledge-game-engine.md §3.5 "Ride decision window and automatic burn" with its invariant "unresolved player decisions are automatically burned when the team's Action Phase closes" — while domain-knowledge.md §10.1 Season-end auto-burn adds a second, season-wide burn rather than replacing the per-phase one. The catalog believes the sources under the source-wins invariant, so the round-scoped unlock is the drift. The code implements the rule's wording: internal/game/internal/core/service/settlement.go, OnRoundCompleted, unlocks the pending rides on rounds 1-37 and burns only when the payload marks the final round.
