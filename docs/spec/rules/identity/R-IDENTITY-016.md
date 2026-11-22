---
id: R-IDENTITY-016
title: An unactivated account is refused at every gated route
statement: |
  An account that has not completed activation may authenticate, but it may do nothing
  else: every gated route refuses it before any permission is even resolved, with a
  refusal that says the account must be activated. Activation is therefore the gate
  between holding a credential and being allowed to act.
module: identity
entity: user
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
  - site: internal/kernel/httpx/errors.go::AuthenticationRequired
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
    - docs/spec/features/identity/access.feature::An unactivated account is refused at every gated page
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  it is one branch of the same gate function, so deleting it cannot let the activation check pass an anonymous caller silently — the gate would answer 403 instead of 401 and the e2e ladder assertion fails first.
---

## Notes

The rule is identity's (activation is identity's fact) but it is enforced in the platform: the permission middleware consults the activation flag the authentication step carried, which is exactly the "enforcement site owned by another layer" case the schema asks to record honestly. Authority is the handler — the HTTP gate is the only place the decision is taken — deviating from the policy default (domain) because the domain holds no operation that refuses work on an unactivated account; the flag is carried, not consulted, below the transport. The fact travels a chain worth naming: users.activated (000002, NOT NULL) → model.User.Activated → httpx.AuthUser.Activated (built by identity.Module.UserForToken) → this branch. The rule covers exactly the routes that carry a gate — and since Phase G (§6 item #10) every internal/game route does (the six /v1/rides routes name players:read / players:write), so an unactivated — or anonymous — caller is refused there too, before the handler runs and before any idempotency key is claimed; the Phase B coverage boundary this note recorded is closed, and the registrations that carry the gates are naming sites catalogued on R-IDENTITY-017. Ordering is deliberate: activation is checked before the role's permissions are read, so an unactivated account never causes a permission query, and the refusal is 403 with "your user account must be activated to access this resource" — distinct from the missing-permission message (R-IDENTITY-017) and from the 401 an unauthenticated request receives. Folded at merge: provisional R-KERNEL-009 stated this fact as the first rung of the permission-gate ladder (anonymous -> 401 AuthenticationRequired, unactivated -> 403 InactiveAccount, missing code -> 403 MissingPermission), so its activation half now lives here and its missing-code half in R-IDENTITY-017 — the kernel sweep's middleware internals and its TestRequirePermissionMiddleware pointer are the site and verification this rule now carries. The 401 writer (httpx.AuthenticationRequired) is listed as defense-in-depth:
