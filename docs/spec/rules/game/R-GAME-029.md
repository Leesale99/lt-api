---
id: R-GAME-029
title: Burning a ride settles its money in the same transaction
statement: |
  Burning a ride settles the money in one step: the ride reaches the burned state, its
  locked tokens are destroyed and credited as TB at one TB per token, the ride's accrued
  bonus is credited as TB on top of that, the currency owed back is returned as the
  lock-time base price (base_at_lock) times the tokens burned, and all of it happens in
  the transaction that flips the state.
module: game
entity: ride
category: orchestration
status: specified-only
authority: service
mechanism: null
write_scope: null
enforcement: []
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/domain-knowledge.md
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 03 §"First transactional operations" defines Burn as "ride burned + TB awarded + currency returned + tokens destroyed"; phase-06's §"Transaction boundary" sequence shows the same ordering inside one PostgreSQL transaction (game validates, the ledger posts, game flips the ride, COMMIT). Only the state flip is real today: both the player burn command and the season-end force-burn call the ledger's Burn intent, which writes nothing (no TB, token or currency balance exists; the rides row keeps its money columns on a burned arrival by design — 000013 deliberately does not couple burn). Authority is the service because the ordering across the game/ledger seam is the use case's, not the domain's. The credit is now stated in domain terms, not as "value": TB is one per burned Token plus the ride's acc (domain-knowledge.md §5 Assets and Economy, Core assets — "Each Token burned contributes 1 TB, with the ride's acc added on top"), while base_at_lock prices only the Currency return (domain-knowledge-game-engine.md §5.6 Burn a Ride: tb_credit = tokens_locked + acc, currency_credit = base_at_lock × tokens_locked); the two formulas agree because base_at_lock never scales the TB credit. Numbered from the phase-doc sweep (was provisional R-GAME-202).
