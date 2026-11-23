---
id: R-IDENTITY-019
title: An account is born unactivated
statement: |
  A newly registered account is never active: activation is a separate, token-driven step
  that the user completes later from the mail. The flag a caller supplies is overwritten on
  the way in, so no registration — however framed — can create a working account.
module: identity
entity: user
category: invariant
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/service/activation.go::Service.Register
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/handler/dto.go::registerUserInput
    layer: handler
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.Insert
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 400
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState
    - internal/e2e/users_test.go::TestRegisterUserHandler
  gherkin:
    - docs/spec/features/identity/account.feature::A newly registered account arrives unactivated
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  if the service stopped forcing false, TestRegisterUserHandlerSuccessState's "new user must not be activated" assertion fails first — it reads the persisted row, not the response.
---

## Notes

Authority is the service, deviating from the invariant default (database): the flag is set by the use case — Register overwrites whatever the caller put on the entity with false before the write — and the database only demands that some value is present (000002's activated boolean is NOT NULL with no default, so it pins neither true nor false). That is a real deviation worth stating: were the overwrite removed, a caller that could reach the service would create an active account and the whole activation gate (R-IDENTITY-016) would be moot. The other two sites are copies that fail loudly: the request shape has no `activated` field, so a body carrying it is an unknown key (400, pinned by TestRegisterUserHandler's "unknown fields rejected" case with both `activated` and `id`), and Insert binds only what the service handed it. Coverage gap: the HTTP layer is the only place this is pinned; there is no service-level test of Register (the service package tests activation only).
