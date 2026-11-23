---
id: R-LEDGER-007
title: Buying tokens posts both sides of the purchase
statement: |
  Buying tokens is one movement with two sides: the player pays currency into the common
  pool and receives tokens from the team reserve, and both sides are posted together, so
  no leg of a purchase can survive on its own.
module: ledger
entity: journal_entry
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
expert-ref: docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 06 §"Double-entry idea" gives the four legs of the purchase (player currency -10, common pool +10, player tokens +10, team reserve -10) and §"Done when" requires tracing a buy end to end. The doc names no atomicity word here, but pairs cannot commit half-posted without breaking R-LEDGER-002, and the ordering across the legs is the use case's — hence authority: service. Nothing exists in code: no reserve, wallet, pool or currency table (grep for BuyFromReserve / TransferCurrency / reserve finds only comments). Doc-vs-doc note: phase-03's §"First transactional operations" names the same operation BuyFromReserve with three legs and no common pool (currency - price, reserve - tokens, wallet + tokens); this fragment follows phase-06 because it is the accounting-shaped doc; the leg count itself is a product decision the catalog records rather than settles.
