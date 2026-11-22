---
id: R-GAME-034
title: A registration is born with its starting currency
statement: |
  Registering a player creates the season registration and grants the starting currency
  in one transaction: a registration that exists without its starting currency is not a
  state the system can reach.
module: game
entity: player
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
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge.md
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 03 §"First transactional operations" defines Register as "player created + starting currency + season registration" — one business operation, one transaction per §"Tasks 2-3". The registration half is real: Service.PlayerCreate checks the season and the favourite team, stamps the authenticated principal and inserts the player in one statement (identity's Register creates the user in a different context). The currency half does not exist: no balance, wallet or currency table anywhere, and no grant step in either use case. Where the grant belongs — identity's Register or game's PlayerCreate — is undesigned in the doc, which is why only the atomicity is catalogued and no authority beyond the use case is asserted. The money side is R-LEDGER-004/007's design. Numbered from the phase-doc sweep (was provisional R-GAME-207).
