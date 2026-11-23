---
id: R-IDENTITY-014
title: Two roles and eleven permission codes are seeded reference data
statement: |
  The vocabulary of what a caller may do is fixed: eleven codes covering reading and
  writing the calendar, the players and the teams, plus the right to manage other
  people's players. Two roles exist — administrator, which holds all eleven, and the
  standard user, which holds the read codes plus the right to manage its own player.
  Nothing in the running service adds, removes or re-grants a permission.
module: identity
entity: permission
category: policy
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
    - roles_name_key
  sentinel: null
  http: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/auth_test.go::TestRequirePermissionMiddleware
    - internal/e2e/rides_test.go::TestRideRoutesPermissionGate
  gherkin:
    - docs/spec/features/identity/access.feature::The standard user may read the calendar and manage her own player
    - docs/spec/features/identity/access.feature::The administrator may do everything
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the policy default (domain) because the matrix is data, not a computation: the eleven codes, the two roles and the twenty-two grants are rows an applied migration inserts, and the running service has no repository method that writes them — permissions are read-only to the application (PermissionRepository has exactly one method, GetAllForRole). The mechanism is `constraint` in the schema's closest sense: what pins the seed is the keys (a duplicated code is a 23505, a duplicated role name is a 23505) and the immutability of applied migrations, so the fix for a wrong matrix is a new migration, never an edit. The duplicate-key refusals are R-IDENTITY-013's rule, not this one's: its sabotage scenario proves permissions_code_key and its grant scenario proves roles_permissions_pkey, while roles_name_key stays unproved. The concrete matrix: admin = seasons, rounds, matches, players, teams (read and write) plus players:write:any; user = seasons:read, rounds:read, matches:read, players:read, players:write, teams:read. The split between the two roles is the actual product policy — a standard user may register and manage their own player but may not touch anyone else's, which is why players:write is granted and players:write:any is not. The matrix itself is proved by two scenarios: "The standard user may read the calendar and manage her own player" pins the exact six-code set and that the role carries no other code, and "The administrator may do everything" pins all eleven, so the split above is executable rather than prose. The e2e permission gate (TestRequirePermissionMiddleware) stays the other half: it pins that the user role carries teams:read and lacks teams:write from the middleware's side. Coverage gap: nothing asserts that the running service cannot mutate the matrix — that rests on there being no repository method that writes it. The ride surface names no ride-specific code — the vocabulary stays closed at eleven — and the six /v1/rides routes are gated with players:read (the two reads) and players:write (the four commands): the ride surface is the player domain's action surface (a ride spends a named player's tokens), and players:write is the only write code the standard user role holds, so the lock flow stays open to an activated standard user.
