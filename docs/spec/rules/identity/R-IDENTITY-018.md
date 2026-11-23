---
id: R-IDENTITY-018
title: A failed login is one refusal, whichever half failed
statement: |
  An email that no account holds and a password that does not match the account produce
  the same refusal: the same status, the same body, and the same sentence. The service
  never says which half was wrong, so a caller cannot use login to learn whether an
  address is registered.
module: identity
entity: user_token
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/service/activation.go::Service.Authenticate
    layer: service
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrInvalidCredentials
  http: 401
fixture: null
verification:
  unit:
    - internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip
    - internal/identity/internal/service/authenticate_test.go::TestAuthenticate
  e2e: null
  gherkin:
    - docs/spec/features/identity/access.feature::An unknown email is refused as an invalid credential
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the service, deviating from the policy default (domain): the equivalence is created by the use case — an unknown email is translated into the same sentinel a wrong password produces (ErrInvalidCredentials), so nothing above it can tell the two apart and the handler has exactly one branch to map. The domain contributes the other half of the mechanism and cannot drift quietly: password.Matches reports a wrong password as a plain (false, nil) instead of bcrypt's sentinel error, so the service has no error to branch on — TestPasswordHashRoundTrip pins exactly that, including that a zero-value password is a genuine error rather than a silent mismatch. Refusal: 401 with "invalid authentication credentials", the same for both halves. Known residual: the equivalence holds in the refusal, not in the work done — an unknown email returns before any hash comparison, so the two failures have measurably different latency (the comment's "the same code path" is true at the level of results, not of timing), and a timing observer can still enumerate addresses. Closing it needs a dummy comparison on the unknown-email path; until then this should be reported as a candidate defect rather than a held rule. Coverage gap, recorded knowingly: there is no test at all for login — no service test and no e2e test for the authentication-token endpoint — so neither half of the equivalence is pinned end to end. Note the contrast that makes the risk concrete: the activation-token request endpoint answers 404 for an unknown email, so identity is enumeration-resistant on one endpoint and not on the other.
