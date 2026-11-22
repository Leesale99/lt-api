---
id: R-IDENTITY-001
title: Identity requests are framed before anything else happens
statement: |
  A registration must carry a non-empty name of at most five hundred bytes and
  an email that is non-empty and syntactically well formed. Every request that
  carries an email — registration, activation-token request and login alike —
  is held to the same email shape. A request that fails is refused with the
  field errors and creates or changes nothing.
module: identity
entity: user
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/model/user.go::ValidateRegistration
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/model/user.go::ValidateEmail
    layer: validator
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 422
fixture: null
verification:
  unit:
    - internal/identity/internal/model/user_test.go::TestValidateRegistration
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Request framing, so the authority is the transport edge (category default), and the layer is `validator` because the check lives in a `ValidateX` function the handler calls before the use case — the schema's own split (D3). Two sites, one framing step: ValidateRegistration is the registration shape (name bounds plus email plus password) and ValidateEmail is the standalone shape the token-request and login handlers call. Neither is a copy of the other — removing ValidateEmail drops the email check on two of the three flows. Callers: Handler.registerUser, Handler.createActivationToken, Handler.createAuthenticationToken (internal/identity/internal/handler/users.go, internal/identity/internal/handler/user_tokens.go); the handlers are where the 422 is written, which is why no handler site is listed (the seeds treat status mapping the same way). No phase document states identity's framing rules, so expert-ref is omitted. Validators keep collecting every bad field, so one bad field never hides another (a case TestValidateRegistration pins).
