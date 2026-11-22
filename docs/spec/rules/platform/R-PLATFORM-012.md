---
id: R-PLATFORM-012
title: Each context owns one schema and speaks only as its own role
statement: |
  Each bounded context owns one database schema and nothing else: the calendar its
  teams, seasons, rounds and matches; the game its rides, players and idempotency keys;
  identity its users, tokens, roles and permissions. A context reaches the database as
  its own role, which holds privileges on its own schema alone, so an attempt to read
  or write another context's tables is refused by the database itself rather than by
  code review.
module: platform
entity: context_schema
category: boundary
status: implemented
authority: composition-root
mechanism: null
write_scope: cross-table
enforcement:
  - site: migrations/000015_split_context_schemas.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: migrations/000018_context_roles.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
refusal:
  code: "42501"
  constraint: null
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/testdb/boundary_test.go::TestGameAppBoundary
    - internal/testdb/boundary_test.go::TestScheduleAppBoundary
    - internal/testdb/boundary_test.go::TestIdentityAppBoundary
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
stale_pointer: null
tie: null
---

## Notes

Authority is the composition root (ADR-028): only it decides which pool a context receives, and the pools are the boundary's runtime half — the migrations are the other (schemas own the tables, 000015; one role per context with CRUD on its own schema only, 000018): two authoritative sites for two disjoint facts — which schema holds which table, and which role may touch it. The runtime half (each pool's SET ROLE pairing, and the rule that no context is ever handed another context's pool) is a different authority — the Go connection hook — and is catalogued separately as R-PLATFORM-015 precisely because the privilege and the pool's role are enforced in two different places. Deliberately NOT re-catalogued here: 000018's two SECURITY DEFINER round guards are already the defense-in-depth sites of R-GAME-008 — this rule is the role/schema separation itself, not the ride gate. game_app therefore holds USAGE on the schedule schema and EXECUTE on those two functions, and no table privilege at all. Refusal surface: SQLSTATE 42501, unmapped in Go — a boundary violation that ever reached a handler would be a generic 500. The boundary suite connects AS the roles directly, so it pins these two migrations and says nothing about how production acquires a role (that gap is recorded on R-PLATFORM-015).
