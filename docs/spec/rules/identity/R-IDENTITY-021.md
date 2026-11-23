---
id: R-IDENTITY-021
title: Identity's tables are reachable only through identity's own database role
statement: |
  Who logs in and what they may do lives in its own schema, writable and readable only by
  the role that speaks for that context. Every other context's role is denied on those
  tables, and identity's role is denied on theirs, so a cross-context read cannot sneak in
  through SQL even if someone forgets the import rule.
module: identity
entity: module
category: boundary
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: migrations/000018_context_roles.up.sql
    layer: repository
    role: authoritative
    pinned_by: null
  - site: migrations/000015_split_context_schemas.up.sql
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: "42501"
  constraint: null
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/testdb/boundary_test.go::TestIdentityAppBoundary
    - internal/testdb/boundary_test.go::TestIdentityAppInsertAllowed
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
knowledge-ref: null
stale_pointer: null
tie: |-
  the copies fail loudly — TestIdentityAppBoundary asserts identity_app can read its own five tables and is denied on game.players and schedule.teams, and TestIdentityAppInsertAllowed asserts it may write its own rows, so a widened grant shows up as a failing "denied" assertion first.
---

## Notes

Boundary, and the authority is the composition root rather than the database — the grants are Postgres's to enforce, but the decision they encode (which context owns which tables, and the direction of every reach) is wiring, recorded in a migration and exercised by the test harness, not a predicate any write could violate. The layer value is `repository` by the catalog's existing convention for SQL that carries a boundary (R-GAME-008 lists 000018 the same way). 000018 is the authoritative site (schema USAGE plus SELECT/INSERT/UPDATE/DELETE on identity's tables for identity_app, and the ALTER DEFAULT PRIVILEGES that keeps future identity tables under the same rule); 000015 is the move itself — users, user_tokens, roles, permissions and roles_permissions leave public — and is defence-in-depth in the sense that without it there would be nothing to grant on. Refusal is Postgres's insufficient_privilege (42501), unmapped in Go because the application never crosses the line: the boundary test is the only code that tries. Two facts worth carrying into the report: the application connects with all three role memberships and SET ROLEs per connection (000018's header), so the boundary is a discipline the pool enforces rather than a privilege the login lacks; and identity_app holds DELETE on identity's tables, so nothing at the privilege level prevents an account deletion path from being added later. Related: R-PLATFORM-013 is the general boundary rule (contexts depend one way and meet only at facades); this rule is its database-grant half.
