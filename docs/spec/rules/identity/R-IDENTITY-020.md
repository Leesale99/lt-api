---
id: R-IDENTITY-020
title: An account's identity facts are stamped by the store
statement: |
  An account's id, its creation time and its version counter are produced by the store,
  never by the caller — a client cannot choose who it is or how old its row looks. The
  store hands them back with the write, so the service never has to invent one.
module: identity
entity: user
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000002_create_users_table.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.Insert
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/handler/dto.go::registerUserInput
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 400
fixture: null
verification:
  unit:
    - internal/identity/internal/repository/user_repo_test.go::TestInsert
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandler
  gherkin:
    - docs/spec/features/identity/account.feature::The database refuses an account that names its own id
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-01-postgresql-persistence.md
stale_pointer: null
tie: |-
  the copies fail loudly in the obvious way: binding a client value to id would make 000002 refuse the INSERT, and TestInsert's "returns the persisted user with server-generated fields" subtest asserts id 1 after a truncated restart, version 1 and a non-zero created_at.
---

## Notes

The database owns these values, not Go — the invariant is that no caller can choose them: id is GENERATED ALWAYS AS IDENTITY (a client-supplied id is not merely ignored, it is refused by the database), created_at is DEFAULT now() at timestamp(0), and version is DEFAULT 1. The repository's part is the read-back — the INSERT ... RETURNING clause is how the stamped values reach the entity — and the DTO's part is that none of the three has a field, so a body carrying `id` is an unknown key (400). The version column's default of 1 is also the first half of R-IDENTITY-010's compare-and-set: the counter starts at one and every write bumps it. Note the timestamp precision is seconds (timestamp(0)) — a fact the catalog should keep because it is why two writes in one second are not distinguishable by created_at. Category note: recategorized from `persistence-fact` for twin-consistency with R-IDENTITY-006 — the same DB-key shape (a store-stamped fact the key itself refuses to take from a caller) — because the sabotage proof is recorded as the scenario above, which the persistence-fact category forbids.
