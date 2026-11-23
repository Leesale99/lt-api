---
id: R-GAME-004
title: Ride money columns stay in their domain
statement: |
  A ride carries a positive whole token stake, a positive base price captured at lock
  time, a non-negative accumulated bonus and a non-negative streak. There is no
  representable ride with zero or negative money.
module: game
entity: ride
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000006_create_rides_table.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::ValidateRide
    layer: domain
    role: advisory-copy
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::insertRide
    layer: repository
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - rides_tokens_locked_check
    - rides_base_at_lock_check
    - rides_bonus_acc_check
    - rides_streak_check
  sentinel: ValidationError
  http: 422
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_validation_test.go::TestValidateRide
    - internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_InsertCheckViolations
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandler
  gherkin:
    - docs/spec/features/game/settlement.feature::A stake of nothing is refused and no ride is born
    - docs/spec/features/game/settlement.feature::The database refuses a ride carrying money outside its domain
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Four CHECKs, one rule: the bounds are the same kind of fact, so they are catalogued together (constraint names stay individually mappable). The Go copies cover only `tokens_locked` (the request edge's own field, so the caller reads a clean 422 on the field) — base_at_lock is server-stamped from the ledger's token lookup (a stub — the ride handler hard-codes the base), and acc/streak are written by the ledger and the domain, never by the request. The store maps every one of the four by name: a 23514 becomes a domain.ValidationError naming the field the column bounds and the violated check (``tokens_locked: violates rides_tokens_locked_check`` …), which the handler renders as the module's 422 — without the mapping the four fall through as raw driver errors, so a caller that reaches them gets a 500. The ride INSERT is the only statement in this store that carries money — every UPDATE deliberately omits the money columns, the ledger's own write owns them — so insertRide is the one classification site; a money write that bypasses it is the ledger's to classify (R-LEDGER). No facade scenario can reach the mapping: the request edge refuses the only input-side money field (tokens_locked) before the store, so the transport half is pinned at the adapter (TestRideRepository_InsertCheckViolations) and the database half by the sabotage outline in settlement.feature — docs/spec/gherkin-style.md §10 ("What does not belong in a feature") records that a rule provable only by reaching into a package's internals is recorded in the catalog, not invented as a step.
