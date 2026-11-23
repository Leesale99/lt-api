---
id: R-GAME-035
title: A registration the rides still name is not withdrawn
statement: |
  A withdrawal removes the season entry only while nothing else points at it: a
  registration that still carries rides cannot be deleted, because the rides' history
  would be left naming a player that is gone. The caller is told the registration is
  still referenced, never only that the server failed.
module: game
entity: player
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: cross-table
enforcement:
  - site: migrations/000006_create_rides_table.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Delete
    layer: repository
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23503"
  constraint:
    - rides_player_id_fkey
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/adapter/outbound/postgres/player_repo_test.go::TestPlayerRepository_Delete
  e2e:
    - internal/e2e/players_test.go::TestDeletePlayerHandler
  gherkin:
    - docs/spec/features/game/players.feature::A player still carrying rides is refused as a registration in use
    - docs/spec/features/game/players.feature::The database refuses to withdraw a registration its rides name
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The reference is the game context's own (rides_player_id_fkey, ON DELETE RESTRICT), so the delete itself can never succeed while a ride names the player; the repository site is an advisory copy that only turns the driver's 23503 into the module's ErrRecordInUse so the caller reads the record-in-use 409 instead of a 500. Catalogued as its own rule rather than folded into R-GAME-011, whose group it sat under as the withdraw command's surface: R-GAME-011 owns *who* may withdraw (ErrForbidden, 403) and a rule carries one refusal, so the referential refusal gets its own entry, the way R-SCHEDULE-025 records the calendar's team half. The season-delete composition asks the same FK through DeleteForSeasonTx and is R-SCHEDULE-027's refusal, not this one.
