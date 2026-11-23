---
id: R-LEDGER-002
title: Every money movement posts a balanced journal pair
statement: |
  Every movement of money is recorded as a pair of journal entries whose debits equal
  their credits, in the same transaction as the balance projection update. A
  single-sided entry never commits, so the journal always explains every balance.
module: ledger
entity: journal_entry
category: invariant
status: specified-only
authority: database
mechanism: trigger
write_scope: cross-row
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

Specified only (phase 06: double-entry idea + "the ledger owns journal consistency"). Scope is cross-row — the predicate is "the entries of one journal group sum to zero", which no row-local CHECK can express, so the taxonomy's sub-order points at a deferred constraint trigger (or a schema in which entries can only be appended in pairs, with the sum exposed as a view). Recorded here as cross-row precisely so nobody tries to write it as a CHECK on a single row. Refusal surface undesigned.
