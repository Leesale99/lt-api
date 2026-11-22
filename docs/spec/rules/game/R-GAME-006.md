---
id: R-GAME-006
title: A win accrues tokens × (odds − 1) × (1 + 0.20 × streak)
statement: |
  Winning a ride accrues a bonus equal to the locked tokens, multiplied by the
  team's decimal odds minus one, multiplied by a streak multiplier of
  one plus twenty percent for each consecutive win already banked. A loss or an
  unlock forfeits the whole accrued amount; the formula changes with the product, so
  it lives in the domain and never in SQL.
module: game
entity: ride
category: policy
status: implemented
authority: domain
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/domain/ride.go::BonusDelta
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::CalculateBonus
    layer: domain
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_bonus_test.go::TestCalculateBonus
    - internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded
  e2e: null
  gherkin:
    - docs/spec/features/game/settlement.feature::A win accrues the stake times the odds minus one times the streak multiplier
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: null
---

## Notes

One formula, two entry points: BonusDelta is the one match's increment (what the ledger's AccrueBonus applies) and CalculateBonus is delta + acc (what the in-memory ride shows). They are listed as two authoritative sites because the accrual total and the delta must never disagree — BonusDelta is the statement, CalculateBonus delegates to it. The ledger owns the write, not the arithmetic (its AccrueBonus takes the delta). streakRate is a ≤2-dp decimal by contract (ADR-021): widening it re-opens silent rounding, and bonus_acc's scale assumes the budget. No phase document covers this rule; the contract lives in the code comments and ADR-021, so expert-ref is omitted rather than pointed at a doc that does not state it.
