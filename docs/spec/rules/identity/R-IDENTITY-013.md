---
id: R-IDENTITY-013
title: Every permission code is unique and every grant is one role-permission pair
statement: |
  A permission is identified by its code, and a code exists once: there is one row per
  thing a caller may do. A grant says "this role carries this permission", at most once
  per pair, and both sides of the pair must exist. So "does this role carry this code"
  has exactly one answer.
module: identity
entity: permission
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000004_add_permissions.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
refusal:
  code: "23505"
  constraint:
    - permissions_code_key
    - roles_permissions_pkey
  sentinel: null
  http: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/auth_test.go::TestRequirePermissionMiddleware
  gherkin:
    - docs/spec/features/identity/access.feature::The database refuses a permission code the seed already carries
    - docs/spec/features/identity/access.feature::The database refuses the same grant written twice
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Three declarative facts in one rule because they are one shape — the key of a permission and the key of a grant: permissions.code UNIQUE, roles_permissions' primary key on (role_id, permission_id), and the two foreign keys that keep a grant from naming a role or a permission that does not exist. All are constraints, so the database owns them and every writer is bound by them. Scope is row-local for the uniqueness half; the two foreign keys are the cross-table half, recorded here rather than split out because the refusal surface (a 23505 for the duplicate, a 23503 for the dangling reference) is the same rule: "there is one row per fact". Refusal is unmapped: nothing in Go translates these codes, and the app has no write path to these tables at all (they are seeded by an applied migration), so an http status would describe a surface clients cannot reach. Verification: both uniqueness halves are proved by sabotage scenarios that attempt the forbidden write through the owner pool and assert the named refusal — a duplicate code is refused by permissions_code_key, a repeated grant by roles_permissions_pkey — so dropping either key fails a scenario instead of only a prose claim. Gap, recorded knowingly: the cross-table half is unproved — no test attempts a grant naming a role or a permission that does not exist, so dropping either foreign key would leave every test green. A third sabotage scenario asserting the 23503 closes it, the same deferred gap the catalog records for game's ride CHECKs. Case sensitivity is worth stating: codes are compared exactly, so 'Teams:read' and 'teams:read' would be two permissions, and only convention keeps the vocabulary lowercase.
