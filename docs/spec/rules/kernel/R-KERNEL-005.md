---
id: R-KERNEL-005
title: The database budget pays for storage, not for hashing
statement: |
  A request's storage work is measured by one budget of three seconds, and that budget
  opens only around the database calls it pays for. Work that is not storage — a
  password hash on registration, a password comparison on login — runs outside it, so
  a slow machine cannot turn a healthy write into a timeout.
module: kernel
entity: db_budget
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/persistence/consts.go::DBTimeout
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.Register
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.Authenticate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/middleware.go::Middleware.Authenticate
    layer: handler
    role: defense-in-depth
    pinned_by: null
  - site: internal/kernel/httpx/middleware.go::Middleware.RequirePermission
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/identity/internal/service/db_budget_test.go::TestRegisterHashesOutsideDBBudget
    - internal/identity/internal/service/db_budget_test.go::TestAuthenticateComparesOutsideDBBudget
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the middleware sites wrap exactly one store lookup each, so no CPU-bound work can leak into their window, and every call site (the middleware, every module handler, both identity use cases) draws the one constant, so the budget's value cannot drift between modules.
---

## Notes

Stated in the kernel (one constant, one comment: "a database budget, not a request budget") and split into place by the use cases: Register hashes first and shares one window between its two store writes; Authenticate gives each store call its own window and runs the bcrypt compare between them, untimed. The two identity handlers that deliberately open no budget at all (users.go::createUser, user_tokens.go::createAuthenticationToken) are the negative half of the same rule — they are the sites that would silently spend the budget on crypto if the service stopped timing its own sections, so they are listed in this note rather than as enforcement. Two probes now pin the service half: Register's bcrypt hash and Authenticate's comparison are asserted to run while no database budget is open, so a regression that moves either inside dbCtx fails a test. Silent-refusal surface: an exceeded budget surfaces as a 500, not as a named refusal.
