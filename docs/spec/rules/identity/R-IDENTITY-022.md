---
id: R-IDENTITY-022
title: Activation happens once and asking discloses nothing
statement: |
  Activation happens once. An account that is already active cannot be activated again,
  and a token that was already spent, has expired or was minted for another purpose is no
  longer an activation credential. Asking for a fresh activation mail is answered the same
  way whoever asks: an address no account holds and an address whose account is already
  active receive exactly the answer a genuine request receives — the same status, the same
  body — and neither mints a token nor sends a mail, so the endpoint never says whether an
  address is registered or whether its account is active.
module: identity
entity: user
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/service/activation.go::Service.RequestActivationToken
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/handler/user_tokens.go::Handler.createActivationToken
    layer: handler
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.Activate
    layer: service
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrNotFound
  http: 422
fixture: null
verification:
  unit:
    - internal/identity/internal/service/activation_test.go::TestActivate
    - internal/identity/internal/service/activation_test.go::TestRequestActivationToken
  e2e: null
  gherkin:
    - docs/spec/features/identity/account.feature::An activated account collects no second activation mail
    - docs/spec/features/identity/account.feature::An address no account holds is answered like a registered one
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the service site cannot drift silently — dropping the check would let an active account collect activation mails, which nothing else would refuse (the flag is already true, so the flip is a no-op write that would still succeed), and TestRequestActivationToken pins that the already-active verdict mints no token. if it reintroduced a distinguishable status, the account feature's two neutralization scenarios fail first — each compares the answer byte for byte with the answer a known unactivated account receives, and asserts no mail was booked for the ineligible address.
---

## Notes

Authority is the service, deviating from the policy default (domain): the verdict is a use-case reading (is this account already active?) taken before any token is minted, and no domain operation knows about an account's activation state. The second service site is the other half of "once": Activate's guarded write and the token cleanup (R-IDENTITY-009) are what make a spent token worthless, and the activation test's unknown/expired/out-of-scope subtest pins that a token which no longer resolves is ErrNotFound rather than a new activation. The endpoint is deliberately existence-neutral: the service still forms its two verdicts (ErrNotFound for an unknown email, ErrAlreadyActivated for an active account) and mints nothing and mails nothing for either, but the handler answers both with the very envelope a successful request receives — 202 and "an email will be sent to you containing activation instructions" — so neither the status nor the body carries an existence bit. Neutralizing only the unknown-email half would not have been enough: a 422 for an active account still says the address is registered and its account active, so the oracle would have moved rather than closed. The handler site is that disclosure boundary and is a copy, not a second decision: Nothing else is weakened: the token TTL (R-IDENTITY-007), the scope and single-use lookup (R-IDENTITY-008, R-IDENTITY-009) and the refusal of a spent activation token at Activate are untouched, and the refusal catalogued here is that Activate refusal (422, "invalid or expired activation token"). The request half has no refusal left to catalogue — its two ineligible causes are answered as success — and calling that answer a small lie ("an email will be sent") is the accepted price of the neutral answer: the alternative is an account oracle. Coverage: TestRequestActivationToken drives the request use case's three outcomes and their token side effects; the gherkin scenarios drive the same cases through the HTTP handler. Renumbered at merge (was provisional R-IDENTITY-023): R-IDENTITY-021 was folded into R-PLATFORM-013.
