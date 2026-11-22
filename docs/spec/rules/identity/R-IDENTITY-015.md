---
id: R-IDENTITY-015
title: A caller's permissions are its role's, resolved on every request
statement: |
  Permissions hang off roles, never off accounts: there is no per-account grant and no
  cached set, so two accounts holding the same role are indistinguishable to the gate and
  a grant change takes effect on the next request. An account whose role carries nothing
  resolves to an empty set rather than to an error.
module: identity
entity: permission
category: policy
status: implemented
authority: repository
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/repository/permission_repo.go::PermissionRepository.GetAllForRole
    layer: repository
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/auth_test.go::TestRequirePermissionMiddleware
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Repository authority (deviating from the policy category's domain default), because the rule is a statement about the store's shape: the permission set is the three-table join through roles_permissions keyed on the account's role_id, and there is no table in which an account could be granted something on its own. The read happens per gated request with no caching, which is a deliberate consequence — it is one extra query per gated request in exchange for revocation being immediate, and the middleware times it out on the same DB budget as every other store call. Refusal: null, because a resolution never refuses; the empty set becomes a 403 at the gate (R-IDENTITY-017). Verification gap: there is no unit test for GetAllForRole at all — the only coverage is the e2e gate, which needs a live database and a seeded matrix. A repository test (a role with no grants → empty set and no error; two roles → no cross-contamination) closes it. Category note: `policy`, not `persistence-fact` — the decision is authorization resolved correctly on every request (freshness is part of the rule), and it is proved by the e2e gate rather than a unit test, which the persistence-fact category now requires (policy 14).
