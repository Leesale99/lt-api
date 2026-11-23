---
id: R-IDENTITY-017
title: A gated route requires the exact permission code it names
statement: |
  A gated route is served only to a principal whose role carries the one permission code
  the route names; anything else is refused before the handler runs. The resolved set
  travels with the request so a handler can ask about further codes without a second
  lookup, and the codes are matched exactly, one at a time.
module: identity
entity: permission
category: policy
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/httpx/middleware.go::Middleware.RequirePermission
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/context.go::Permissions.Include
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/errors.go::MissingPermission
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/routes.go::Handler.RegisterRoutes
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 403
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/auth_test.go::TestRequirePermissionMiddleware
    - internal/e2e/rides_test.go::TestRideRoutesPermissionGate
  gherkin:
    - docs/spec/features/identity/access.feature::A code the standard user's role half carries is refused
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the module route tables name the code each route requires (gate("players:write", ...)), and a registration that drops its gate leaves the route ungated rather than mis-coded — the e2e ladder (TestRideRoutesPermissionGate plus the /v1/teams rungs in TestRequirePermissionMiddleware) fails first on the anonymous and unactivated rungs, which would receive a handler status instead of 401/403; same shape as R-IDENTITY-016's accepted Tie on httpx.AuthenticationRequired.
---

## Notes

Authority is the handler, deviating from the policy default (domain): authorization is a transport verdict here — the module route tables compose each handler with its code (gate("teams:write", ...)), the middleware resolves the set and compares, and no domain operation re-checks it. The middleware stays identity-free by design: it declares httpx.PermissionLookup and the composition root hands it identity.Module, which is why the site is in the kernel while the rule is identity's. Split from R-IDENTITY-016 against the letter of D4 (same authority, same function): the two branches decide different facts (a boolean on the account row versus a set derived from the role) and refuse with different messages, and each is independently falsifiable — deleting one branch leaves the other's test green. Reviewed and kept apart because merging would give one rule two client-facing messages and three status classes. Matching is exact and per-code (slices.Contains on the resolved slice), so there is no wildcard, no prefix matching and no hierarchy: a role that should be able to write must be granted the exact write code. Handler-level second asks (players:write:any) read the same carried set through httpx.GetPermissions, which returns an empty set when the gate never ran — so a handler that asks beyond its route's code silently sees "no permission" rather than an error. Verification gap: the rule is only exercised end to end; there is no unit test of the middleware with a fake lookup, so a change to the gate's order or its carried set is only caught by the e2e matrix — the two /v1/teams rungs plus the six-route /v1/rides ladder (anonymous, unactivated, code-less role, admitted standard user). The permission-gate ladder's carried-set and refusal-name sites are here — httpx.Permissions.Include is the exact-match lookup a handler asks with, and it is also what makes the carried set usable beyond the route's own code; httpx.MissingPermission is the 403 writer this rule's refusal names. R-IDENTITY-016 carries the ladder's other two rungs (anonymous -> 401, unactivated -> 403).
