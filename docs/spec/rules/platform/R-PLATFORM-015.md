---
id: R-PLATFORM-015
title: A context's pool speaks as that context's role
statement: |
  The connection pool a context is given runs every one of its statements as that
  context's role, and no context is ever handed another context's pool. The role is
  bound when a connection is established, together with the search path that resolves
  the context's own unqualified table names, so no statement can run with broader
  privileges than the context it serves.
module: platform
entity: context_schema
category: boundary
status: implemented
authority: repository
mechanism: null
write_scope: cross-table
enforcement:
  - site: cmd/api/main.go::openContextPool
    layer: repository
    role: authoritative
    pinned_by: null
  - site: cmd/api/main.go::openPools
    layer: repository
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
    - internal/testdb/context_roles_test.go::TestContextPoolsRunAsContextRole
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (boundary → composition-root): the database half of the boundary is R-PLATFORM-012's, while this is a connection mechanic — the pool hook is the only place that decides which role a context's statements run as, which makes it a repository decision rather than a wiring choice. Both lines are one binding: SET ROLE is what grants the privileges, SET search_path is what lets the stores keep unqualified SQL (role-level settings apply at session start, not on SET ROLE, so the hook must carry both), and each pool is built from its own per-context URL override or from the server's. The migration runner and the seed keep unrestricted connections that are never handed to a repository. The harness now pins the hook's shape: its ContextPool — the deliberate structural copy of cmd/api's openContextPool — is asserted to run every context's statements as that context's role, so a dropped SET ROLE or a mis-wired pool fails a test. The residual stands: production's own hook is verified by structural identity with that copy, not by a direct test.
