---
id: R-LEDGER-001
title: No balance goes negative
statement: |
  A player's currency or token balance can never fall below zero. A debit that would
  overdraw is refused before any journal entry or balance projection is written, so a
  negative balance is not a repairable state — it is unrepresentable.
module: ledger
entity: balance
category: invariant
status: specified-only
authority: database
mechanism: constraint
write_scope: row-local
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

Specified only. No balance table exists yet: the ledger's Repository currently persists the rides row's bonus_acc and nothing else, and LockTokens/Burn are intent seams that write nothing. The design intent from phase 06 is the ledger as the single authority for balance mutation ("game should never UPDATE ledger.balances"), which puts the *decision* in the ledger operation and the *refusal* in the row: a NOT NULL + CHECK (balance >= 0) per balance row, plus the ledger's pre-check for a clean client-facing refusal. Refusal surface undesigned — either a ledger sentinel (409) or a validation error (422); decide when the accounts/journal tables land.
