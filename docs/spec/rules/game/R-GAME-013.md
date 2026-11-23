---
id: R-GAME-013
title: Another context's records are checked when game writes a reference to them
statement: |
  A registration's season and favourite team must exist in the calendar, and a
  ride's team must exist, at the moment game writes them — the cross-context
  foreign keys no longer do it. A missing favourite team is refused on the
  field, a missing season or team elsewhere is not-found, and a registration
  also requires its user to exist in the identity context.
module: game
entity: player
category: boundary
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/game/internal/core/service/player_commands.go::Service.referenceTeam
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerCreate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideCreate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/players.go::createPlayer
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ValidationError
  http: 422
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/service_test.go::TestService_RideCreateUnknownParents
    - internal/game/internal/adapter/inbound/http/players_test.go::TestCreatePlayerNamesTheUserField
  e2e:
    - internal/e2e/players_test.go::TestCreatePlayerHandler
    - internal/e2e/players_test.go::TestUpdatePlayerHandler
  gherkin:
    - docs/spec/features/game/players.feature::A registration naming a team the calendar does not carry is refused
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
stale_pointer: |-
  two repository comments still describe FK-era behaviour after 000016 — ride_repo.go::insertRide's FK branch says "the referenced player, team or match does not exist", though with the cross-context FKs gone the only surviving reference on that INSERT is rides_player_id_fkey (a missing team or match is the port's check), and player_repo.go::GetAll's "favorite_team_id is never 0 in the schema (FK)" names an FK that 000016 dropped (0 still works as the no-filter sentinel).
tie: |-
  the handler site fails loudly — it asks identity for the principal's user row and its refusal keeps the field-error shape, but the user reference is checked *only* there and only when the identity directory was installed (`if h.users != nil`): with the FK gone, a module wired without identity silently plants an orphan user_id.
---

## Notes

Category boundary (the cross-context FKs came out in 000016 so the composition moved into the use cases), but authority is the service, not the composition root, because each use case performs its own check before its write — the deviation from the boundary default is deliberate and this note is the reason. Disjoint cases of one statement, one refusal per case: an unknown favourite team is a field-level 422 on favorite_team_id (the player, not the team, is the resource); an unknown season in the route and an unknown ride team are 404; the ride's match reference is not checked here — it is the round gate's (R-GAME-008) and answers the round refusal. Its 422 used to name favorite_team_id — a leftover of the FK-era shape that mislabelled the failing reference; it now names user_id, the principal's user account (the mislabel half is fixed; the optional-check half is still a defect, not a rule).
