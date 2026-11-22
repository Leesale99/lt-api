---
id: R-IDENTITY-008
title: A token only works for what it was minted for, while it is fresh
statement: |
  A presented token resolves to its account only if it was minted for the purpose the
  presenter is using it for and its lifetime has not run out. Both facts are part of
  the same lookup, so a spent, wrong-purpose or expired token is not a distinguishable
  state — it is simply "no such token", and the caller is refused as unauthenticated.
module: identity
entity: user_token
category: policy
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.getForToken
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/identity/identity.go::Module.UserForToken
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrNotFound
  http: 401
fixture: null
verification:
  unit:
    - internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken
  e2e:
    - internal/e2e/auth_test.go::TestAuthenticateMiddleware
  gherkin:
    - docs/spec/features/identity/account.feature::A sign-in token past its day is refused exactly as one that never existed
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the facade site cannot drift silently — it maps ErrNotFound to httpx.ErrInvalidToken, and if it stopped doing so an expired token would surface as a 500 rather than the 401 TestAuthenticateMiddleware's "expired token is indistinguishable from unknown" case pins.
---

## Notes

Repository authority: the decision is a predicate on the token row (scope and expiry) that needs the fingerprint argument, so it can only live in the lookup SQL — and being path-dependent it is only as good as the set of queries that remember it, which is exactly the repository trade-off the schema describes. Category policy, so the deviation from the domain default needs this note: the *window* is the service's (R-IDENTITY-007) but "is this token still usable, and usable as this" is decided at read time by the lookup, and there is no second path that could answer differently today. The expiry test is `expiry > now()` inside the query, not a Go comparison after the read, which is deliberate: an expired row resolves as a miss, so nothing downstream has to remember to check. One lookup serves both purposes (activation and authentication) with the scope passed in, so one rule covers both flows.
