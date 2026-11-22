---
id: R-IDENTITY-012
title: Registration assigns the standard user role, and a client cannot choose one
statement: |
  An account always holds exactly one role, and an account created through registration
  always holds the standard user role. The client never supplies, hints at or upgrades
  the role — administrator rights are given, never self-granted — and a body that tries
  to carry one is refused before it reaches the store.
module: identity
entity: user
category: policy
status: implemented
authority: repository
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.Insert
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/handler/dto.go::registerUserInput
    layer: handler
    role: defense-in-depth
    pinned_by: null
  - site: migrations/000004_add_permissions.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 400
fixture: null
verification:
  unit: null
  e2e: null
  gherkin:
    - docs/spec/features/identity/account.feature::A registration arrives holding the standard user role
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the two copies fail loudly — the handler's DTO cannot accept a role key without a struct field, and the DTO site plus the constraint make a self-granted admin either a 400 or a 23502/23503.
---

## Notes

Authority is the repository, deviating from the policy default (domain): the role is decided by the INSERT itself, which resolves `(SELECT id FROM roles WHERE name = 'user')` server-side and never binds a caller value, so there is no domain value a caller could reach. Refusal is twofold: the request shape has no field for a role, so an attempt to send one is an unknown-key 400 from ReadJSON's DisallowUnknownFields (pinned for `activated`/`id` by TestRegisterUserHandler's "unknown fields rejected"), and the NOT NULL foreign key added by 000004 refuses any row that arrives without a role. Coverage gaps (two): no test asserts the role a registration actually receives (TestInsert exercises the INSERT but never reads role_id back, so pointing the SELECT at the 'admin' role would keep the suite green), and no test drives "role" as an unknown body key. One hazard worth naming: if the 'user' role row ever went missing, the sub-select yields NULL, 000004's NOT NULL refuses the insert with 23502, and the service returns a 500 — the client would see a server error for a missing reference row. Fixtures: a roleAssignmentRule asserting the inserted role_id equals the 'user' row, run through the repository and through direct SQL, closes both gaps.
