---
id: R-GAME-030
title: Resolving a match updates the team's base price
statement: |
  Resolving a match updates the affected team's base price in the same transaction that
  records the result and settles the rides, so a committed result can never sit next to
  a stale base price.
module: game
entity: team
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
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 03 names ResolveMatch's steps twice ("mark match resolved + update team base + resolve rides" in §"New concept", repeated in §"First transactional operations") and that example is the phase's whole motivation. Nothing in code: teams have no base-price column (migrations/000001_create_initial_game_models.up.sql), no update path exists, and the ride's own base_at_lock is a hard-coded stub at the HTTP edge (internal/game/internal/adapter/inbound/http/rides.go: "TODO: ledgerToken := app.Ledger.Tokens.GetByTeamID" above a literal Base: 1.95). Only the atomicity is catalogued: the base-price formula is the game-engine spec's (domain-knowledge-game-engine.md §6.3, the knowledge-ref), so no formula is invented here. Open design question left by the docs themselves: phase-05's suggested ownership gives base price to game while, as built, teams live in schedule (see the docmap's contradiction candidates). Numbered from the phase-doc sweep (was provisional R-GAME-203).
