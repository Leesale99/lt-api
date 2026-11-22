---
id: R-GAME-017
title: A player write may claim the version it read
statement: |
  A player patch may state the version it read; if the stored registration has
  moved on since, the write is refused as a conflict instead of silently
  overwriting the other change. Without a claim the write still re-checks the
  row's version at the statement, so a concurrent edit can never be lost.
module: game
entity: player
category: policy
status: implemented
authority: service
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Update
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/players.go::updatePlayer
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrEditConflict
  http: 409
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/players_test.go::TestUpdatePlayerHandler
  gherkin:
    - docs/spec/features/game/players.feature::An edit claiming a version the registration has moved past is refused as a stale reading
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  a dropped WHERE would make the lost update possible, but the service's own check still refuses the stale claim; only the no-claim path would regress, which is exactly the branch the E2E "matching/stale X-Expected-Version" cases pin.
---

## Notes

Authority is the service (deviation from the policy default of domain): the comparison is against a version the caller claims, which is request-shaped, not a domain fact. The repository site fails loudly (UPDATE ... WHERE version = $n, zero rows → the same conflict), so it is defense-in-depth without a fixture — The handler site is advisory: it turns an unreadable expected-version header into the same conflict instead of a 400 (a deliberate choice: a header that cannot name a stored version cannot match one). Second, quieter fact in the same use case: the merged row — not the patch — is what is validated and written.
