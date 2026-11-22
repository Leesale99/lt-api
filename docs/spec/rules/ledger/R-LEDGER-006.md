---
id: R-LEDGER-006
title: A money operation repeats without moving money twice
statement: |
  A ledger operation that is retried — after a crash, a restart or a duplicate request —
  applies its movement once: the retry is recognised as the same operation and changes
  no balance and no journal entry.
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

Specified only. Phase 06 §"Important rule" makes idempotency one of the four things the ledger enforces in its one place, and "idempotent financial operations" is a stated research topic of the phase. Today the ledger has no idempotency of its own: the HTTP command's Idempotency-Key (kernel/idempotency, migrations/000010) and the settlement handlers' EventID claim protect the callers, not the operation — a redelivered ledger intent would apply twice, since AccrueBonus is a blind increment (`UPDATE rides SET bonus_acc = bonus_acc + $2`) and LockTokens/Burn are no-ops. The claim belongs to the ledger operation (its own transaction boundary), hence authority: service. Related but distinct: R-KERNEL-001..004 and R-KERNEL-014 (with R-GAME-018) make *HTTP* commands and settlement events idempotent; this rule is about the ledger operation itself, which is the unbuilt half.
