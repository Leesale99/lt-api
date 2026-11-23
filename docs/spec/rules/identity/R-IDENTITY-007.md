---
id: R-IDENTITY-007
title: Activation tokens live three days, authentication tokens one day
statement: |
  A token minted to activate an account is good for three days — long enough for the
  user to find the mail — and a token minted by a successful login is good for one
  day. The lifetime is decided once per flow and stamped on the row at mint time, so
  every token of a kind lives exactly as long as its flow says.
module: identity
entity: user_token
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/service/activation.go::Service.Register
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.RequestActivationToken
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.Authenticate
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin:
    - docs/spec/features/identity/account.feature::Her activation token is good for three days
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the service, deviating from the policy default (domain) because the window is a property of the use case that mints the token, not of the token entity: the two constants live unexported in internal/identity/internal/service/tokens.go (activationTokenTTL = 3 * 24h, authenticationTokenTTL = 24h) and are named only at the three mint sites above, which is the code's own argument for keeping them unexported ("so the values stay unexported and cannot drift from their use sites"). Three authoritative sites on purpose: one per flow, disjoint cases (registration, re-request, login), not copies of one statement. Refusal: null — a lifetime is a value, it refuses nothing; the refusal an expired token produces is R-IDENTITY-008's. Coverage gap: no test pins either duration. Every test seeds tokens with its own time.Hour or -time.Minute, so changing 3 days to 3 hours would keep the whole suite green. A fixture asserting the minted expiry of each flow (Register / RequestActivationToken / Authenticate) against the two constants closes it. The value also has a product consequence worth stating at merge time: an authentication token is not sliding — a session simply ends 24 hours after login, with no refresh path.
