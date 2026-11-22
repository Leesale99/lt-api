---
id: R-LEDGER-005
title: A balance is the journal's projection
statement: |
  A balance is only ever an explanation of what the journal says. Every balance change
  is accompanied, in the same transaction, by the journal entries for it, so a player's
  visible balance can always be traced back to the postings that produced it.
module: ledger
entity: balance
category: invariant
status: specified-only
authority: database
mechanism: trigger
write_scope: cross-table
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

Specified only. Phase 06 §"Double-entry idea" (a movement has two sides, journal entries describe it), task 3 "create balance projections", task 6 "make balance operations transactional", and §"Done when" ("trace a buy, lock, loss, and burn operation from the API to the ledger journal and back to the player's visible balance"). The predicate is an aggregate across two tables (an account's entries vs its projected balance), so no CHECK can state it: the schema's sub-order leaves a deferred check in the posting transaction or a trigger on the balance row — the doc itself does not pick, and also allows the alternative of exposing balances only as a view over the journal. Recorded as cross-table deliberately, as the signal to read before anyone tries a row-local constraint. No tables exist; refusal undesigned.
