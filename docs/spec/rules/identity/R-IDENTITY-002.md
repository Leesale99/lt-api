---
id: R-IDENTITY-002
title: A password is eight to seventy-two bytes, checked before it is hashed
statement: |
  A password presented at registration or login must be at least eight bytes and
  at most seventy-two bytes long. The ceiling is not cosmetic: the password
  hashing step refuses anything longer than seventy-two bytes outright, while the
  comparison step ignores everything past the seventy-second — so an over-long
  password is either a server error or silently interchangeable with its own
  prefix. Both bounds are checked before any hashing runs, which turns the
  over-long case into a field refusal instead.
module: identity
entity: user
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/model/user.go::ValidatePasswordPlaintext
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

Two bounds, one statement, one authority: the interval is one fact about the password field, so splitting it would create two rules nobody can enforce separately. The reason for the ceiling is bcrypt's own behaviour, and it is asymmetric in a way that matters: x/crypto/bcrypt's GenerateFromPassword returns ErrPasswordTooLong above 72 bytes (so hashing first would surface a 500 instead of a 422, and would burn a cost-12 hash on input about to be rejected — the reasoning the validator comment carries), while CompareHashAndPassword keeps the algorithm's historical truncation and compares only the first 72 bytes (so a 73-byte candidate matches its own 72-byte prefix). The ceiling therefore guards both directions: it is the only thing standing between two distinguishable passwords and one. The same validator runs on the login path (Handler.createAuthenticationToken), so the interval is a property of the credential, not only of registration. Both bounds sit at exactly the boundary in the pinned test (72 accepted, 73 refused, 8 refused, "short" refused). The 8-byte floor is a product choice with no lower-level backstop: nothing in storage refuses a weaker hash.
