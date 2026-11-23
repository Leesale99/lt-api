---
id: R-GAME-011
title: A registration is managed by its owner or an admin
statement: |
  The player who registered a season entry may always change or withdraw it;
  any other principal may do so only with the administrative permission. A
  registration that belongs to nobody is admin-only by design, and a
  non-owner's attempt is refused before anything is written.
module: game
entity: player
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/player_commands.go::canManagePlayer
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerDelete
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/players.go::actorOf
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrForbidden
  http: 403
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/players_test.go::TestUpdatePlayerOwnership
    - internal/e2e/players_test.go::TestDeletePlayerHandler
  gherkin:
    - docs/spec/features/game/players.feature::A change to someone else's registration is refused
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the service, which deviates from the policy default (domain): ownership is a use-case fact — the domain Player knows its UserID but not who is asking, so the decision needs the Actor the transport hands in and lives at the use-case door. Three authoritative sites on purpose: the predicate (canManagePlayer) plus the two use cases that apply it to a different command (patch, delete are disjoint cases, not copies). The handler site is advisory only: actorOf turns the request's permissions into Actor and therefore shapes *which* refusal the caller sees, but the authority still refuses. Verification gap: there is no service-level unit test of canManagePlayer — the only pin is the E2E ownership probe (schema policy 3 wants a unit pointer). Ordering fact recorded here because it is part of the same refusal: PlayerUpdate/PlayerDelete check the route's season against the row before the ownership test, so a player of another season answers not-found rather than forbidden (existence is not leaked). Scope limit: no equivalent authorization exists for rides — ride commands carry no actor at all and no route permission gate, so any authenticated principal may spend any player's tokens. That absence is a gap, not a rule.
