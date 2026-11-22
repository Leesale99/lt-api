---
id: R-GAME-021
title: Player requests are framed before the command runs
statement: |
  A registration carries a name of at most two hundred characters and a
  favourite team; a patch may name either field and the *merged* registration
  — not the patch — is what must still be valid. A request whose body is empty,
  malformed or names an unknown field is refused before any use case runs.
module: game
entity: player
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/adapter/inbound/http/players.go::createPlayer
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/players.go::updatePlayer
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/domain/player.go::ValidatePlayer
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate
    layer: service
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ValidationError
  http: 422
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/players_test.go::TestCreatePlayerHandler
    - internal/e2e/players_test.go::TestUpdatePlayerHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The service site is listed as authoritative for the same rule, not as a copy: on update the *merged* row is re-validated inside the use case, because a patch that is valid alone (a nil name) can merge into an invalid row only the service sees — the handler's pre-check covers the create body, the service's covers the merge. That split is why both layers carry a 422 with the same field-error shape (the domain's ValidationError is written verbatim by the transport, so a refusal looks the same whichever layer raised it). Name bounds are duplicated on purpose by ValidatePlayer and the DB's players_name_check; the DB side is a constraint, catalogued with the player entity rather than here. Verification gap: no unit test in the game module drives ValidatePlayer or the merge (the two E2E tables are the only pins); the same validator package is unit-tested for other entities. Renumbered at merge: this rule's draft slot was freed by the two early game merges — the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026 — so the game block was renumbered densely.
