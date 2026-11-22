---
id: R-GAME-020
title: Ride requests are framed before the command runs
statement: |
  A ride command carries an idempotency key and, for creation, a well-formed
  body whose player, team and match are named and whose stake is a whole
  positive number of tokens. A request that fails framing changes nothing: no
  key is claimed, no ride is written, and the caller is told which field was
  wrong.
module: game
entity: ride
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.createRide
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.rideCommandToken
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.listRides
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/idempotency/idempotency.go::ValidateIdempotencyToken
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/ride.go::ValidateRide
    layer: validator
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ValidationError
  http: 422
fixture: null
verification:
  unit:
    - internal/game/internal/core/domain/ride_validation_test.go::TestValidateRide
    - internal/game/internal/core/domain/ride_validation_test.go::TestValidateRideState
    - internal/kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideHandler
    - internal/e2e/rides_test.go::TestRideCommandHandlers
    - internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge
    - internal/e2e/rides_test.go::TestListRidesHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Several authoritative sites because the framed facts are disjoint cases of one statement (body shape for create, key shape for every command, the filter vocabulary for the list), and because two of them are shared validators rather than inline checks — the validator layer is still the edge. The ordering is part of the rule: the key is validated and the body hashed before the body is decoded, so a keyless or oversized request claims nothing and inserts nothing. Only two things are framed here, deliberately: the stake's *sign and wholeness* (it arrives as an integer and becomes a decimal) and the ids' presence. The stake's upper bound, the base price and the accrual are not request facts — base_at_lock is stamped by the server from the ledger's token lookup (a hard-wired stub today) and the accrual is the bonus policy's. Boundaries of the same interface: an unknown JSON field is a 400, an empty or malformed body is a 400, a bad state filter value is a 422 naming the field. Renumbered at merge (was provisional R-GAME-021): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely.
