---
id: R-IDENTITY-004
title: An account response never carries the hash or the internal version
statement: |
  What the service says about an account is its id, when it was created, its name,
  its email, whether it is activated and which role it holds. The stored hash and
  the internal version counter are never part of a response.
module: identity
entity: user
category: boundary
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/handler/dto.go::newUserResponse
    layer: handler
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState
    - internal/e2e/users_test.go::TestRegisterUserHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The frozen wire shape is a presentation decision, so it belongs to the handler: the model carries no json tags at all, and the DTO replicates the wire format field by field, which is what keeps a future model change from silently publishing the hash. The rule is separated from R-IDENTITY-003 because the authority differs (there the hash's form is the domain's; here the leak surface is the transport's) — D4 splits on authority, not on subject. Refusal: null, because a rule about what is *absent* has nothing to refuse; the failure mode is a leak, and the only detector today is TestRegisterUserHandlerSuccessState's leak scan of the 201 body. Coverage gap: no test inspects a read path's body for the hash (the register response is the only account body the suite asserts) and there is no user-read endpoint to inspect. Category note: `boundary`, not `interface` — the rule is about what crosses the transport edge (the hash and the version never leave), and a rule about what is absent has nothing to refuse, which the interface category now requires (policy 14).
