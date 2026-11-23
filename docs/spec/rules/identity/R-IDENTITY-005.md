---
id: R-IDENTITY-005
title: A token is twenty-six random characters
statement: |
  Every token the service mints is twenty-six characters drawn from the crypto-grade
  random source — a hundred and twenty-eight bits and a bit of slack, so a token
  cannot be guessed and two tokens never collide. A presented credential that is not
  that shape is refused before any lookup runs.
module: identity
entity: user_token
category: invariant
status: implemented
authority: domain
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/model/user_token.go::GenerateUserToken
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/model/user_token.go::ValidateUserTokenPlaintext
    layer: validator
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/identity.go::Module.UserForToken
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidToken
  http: 401
fixture: null
verification:
  unit:
    - internal/identity/internal/model/user_token_test.go::TestGenerateUserToken
  e2e:
    - internal/e2e/auth_test.go::TestAuthenticateMiddleware
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the length fact has exactly one home.
---

## Notes

The generator is the authority (it produces the shape) and the two other sites are copies with different jobs. ValidateUserTokenPlaintext is defence-in-depth and cannot drift silently: the generator and the validator both read the named constant plaintextTokenLength, and TestGenerateUserToken asserts the minted length against that same constant, so a change on one side fails that test first — Module.UserForToken is an advisory copy: it refuses a malformed credential as the same 401 an unknown token produces, which only avoids a database round trip (the lookup would have missed anyway), so its drift is a UX/cost divergence and never a security one. Authority deviates from the invariant default (database) because nothing in storage knows what a token should look like: the column is bytea and any length fits. Generation no longer delegates to crypto/rand.Text: the plaintext is minted here — tokenEntropyBytes from crypto/rand, RFC 4648 base32 with no padding, the first plaintextTokenLength characters — so the 26-character/130-bit shape is this package's own contract and a stdlib change to rand.Text's alphabet or letter count can no longer silently make the validator reject freshly minted tokens. plaintextTokenLength is still the single home the generator, the trim and the validator read; TestGenerateUserToken pins the minted length, the base32 alphabet, and that the constants still yield the 26-character shape.
