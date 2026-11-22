---
id: R-GAME-012
title: A registration belongs to the caller and the season in the route
statement: |
  A season entry is created only for the authenticated principal: the request
  can never name a different owner, and the season comes from the route, not
  the body. A create without a principal is refused, never stored ownerless.
module: game
entity: player
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerCreate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/players.go::createPlayer
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrForbidden
  http: 401
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/players_test.go::TestCreatePlayerHandler
    - internal/e2e/players_test.go::TestUpdatePlayerOwnership
  gherkin:
    - docs/spec/features/game/players.feature::A registration made in a season belongs to the member who made it
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the service (deviation from the policy default of domain, because the fact is "who is asking" — the Actor, which the domain never sees). The service stamps SeasonID from the route and UserID from the principal, overwriting anything the caller supplied, and refuses an unauthenticated actor with ErrForbidden. The handler site is advisory: it answers a keyless request with the authentication-required 401 before the use case runs, so the caller sees 401 rather than the service's 403 — the authority still refuses either way. Consequence of both sites: the service's ErrForbidden branch is unreachable through HTTP today (the handler's 401 fires first); it is defence for a non-HTTP caller. Verification gap: the only pins are E2E (the "season takes effect from URL, not body" case and the owner-registration case); no unit test drives Actor directly.
