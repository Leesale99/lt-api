---
id: R-GAME-028
title: Taking the tokens and creating the ride are one operation
statement: |
  Locking a ride and taking the player's tokens are one operation: the token balance
  drops by the locked amount in the same transaction that creates the locked ride, so a
  ride can never exist without its tokens having been taken.
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

Specified only. Phase 03 §"First transactional operations" defines Lock as "wallet tokens - locked amount" plus "ride created", and §"Tasks 2-3" demand transaction boundaries drawn by business operation, not by table. Only the ride half is real: Service.RideCreate writes the row inside one transaction with the idempotency claim and the calendar's write-instant round gate, while the ledger's LockTokens intent writes nothing (internal/ledger/postgres.go) because no wallet or token balance exists — the locked amount lives on the ride row only. Naming collision to be aware of when matching against the code sweep: the doc's Lock is the creation move, while `POST /v1/rides/:id/lock` in code is the continuation re-point on an existing ride (R-GAME-002 / R-GAME-007). Numbered from the phase-doc sweep (was provisional R-GAME-201).
