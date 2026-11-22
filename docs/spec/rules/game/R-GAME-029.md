---
id: R-GAME-029
title: Burning a ride settles its money in the same transaction
statement: |
  Burning a ride settles the money in one step: the ride reaches the burned state, its
  locked tokens are destroyed and their value is credited as TB, and any currency owed
  back is returned — all in the transaction that flips the state.
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
expert-ref: docs/league-tokens-learning-phases/phase-03-transactions-and-atomic-operations.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 03 §"First transactional operations" defines Burn as "ride burned + TB awarded + currency returned + tokens destroyed"; phase-06's §"Transaction boundary" sequence shows the same ordering inside one PostgreSQL transaction (game validates, the ledger posts, game flips the ride, COMMIT). Only the state flip is real today: both the player burn command and the season-end force-burn call the ledger's Burn intent, which writes nothing (no TB, token or currency balance exists; the rides row keeps its money columns on a burned arrival by design — 000013 deliberately does not couple burn). Authority is the service because the ordering across the game/ledger seam is the use case's, not the domain's. Numbered from the phase-doc sweep (was provisional R-GAME-202).
