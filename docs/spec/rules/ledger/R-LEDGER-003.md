---
id: R-LEDGER-003
title: Only the ledger moves money
statement: |
  A balance changes only through a ledger operation. Other modules state what should
  happen to the money — deduct, lock, burn — and never compute or write a balance
  themselves, so there is exactly one place where the corruption rule can be
  enforced.
module: ledger
entity: balance
category: boundary
status: specified-only
authority: composition-root
mechanism: null
write_scope: null
enforcement: []
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 06 §"One authority for balances" ("The ledger owns balance mutation. Other modules request operations.") and §"Important rule" (the game must never run `UPDATE ledger.balances`; it calls a ledger operation such as DeductCurrency), reinforced by task 4 "move all balance writes into the ledger" and task 8 "prove that game code cannot bypass the ledger". What exists is only the seam: internal/game/internal/core/port/ledger.go's four intents over a caller-supplied querier, plus the import matrix that keeps game off ledger's package (internal/arch/arch_test.go). What is missing is the refusal: the ledger owns no schema at all, so "game_app may not write a balance" has nothing to refuse — and the direction is inverted, the ledger's only SQL being `UPDATE rides SET bonus_acc` in internal/ledger/postgres.go (a game table). The doc's design implies a ledger schema plus a ledger-only role; that is wiring and grants, hence authority: composition-root (the boundary default) rather than the database.
