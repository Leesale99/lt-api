---
id: R-LEDGER-004
title: Every balance sits in an account of a known kind
statement: |
  Money is not held as a free-standing number: every balance belongs to an account, and
  every account is one of the ledger's kinds — a player's currency, a player's tokens
  for a team, a team reserve, or the common pool.
module: ledger
entity: account
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

Specified only. Phase 06 §"Start simple" (use `accounts`, `journal_entries`, `balances`; task 1 "introduce account identities") and §"An account could represent", which enumerates the four kinds — player currency, player tokens for a team, team reserve, common pool. The enumeration is the doc's whole account budget, so an extra kind is a design change, not an implementation detail; the predicate is row-local (the kind value), which is why it is a constraint and not a trigger. No such table exists: no accounts/journal/balances anywhere in migrations/, and no ledger schema (000015 creates schedule/game/identity only; 000018's closing comment defers "the Phase 08 outbox, Phase 06 balances" to later migrations). Refusal surface undesigned in the doc.
