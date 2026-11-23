---
id: R-SCHEDULE-023
title: A season or round that has started is durable
statement: |
  A round that is closed, or any round with a match that has already started,
  cannot be deleted; neither can a season that is in progress, closed, or has
  a started match. Deleting would take the history with it, so the calendar's
  past is not removable.
module: schedule
entity: round
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: cross-row
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - round_lifecycle_blocks_delete
    - season_lifecycle_blocks_delete
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/gates_test.go::TestRoundDeleteGateRejectsMatchHistory
    - internal/schedule/internal/repository/gates_test.go::TestSeasonDeleteGateRejectsMatchHistory
  e2e:
    - internal/e2e/rounds_test.go::TestDeleteRoundHandler
    - internal/e2e/seasons_test.go::TestDeleteSeasonHandler
    - internal/e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers
  gherkin:
    - docs/spec/features/schedule/deletion.feature::The database refuses to delete a round with a started match
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Two delete gates, one statement (the round level and the season level, taken from ADR-007 one level up); both ask the same time fact as the freeze gates (round_has_started_match / season_has_started_match) and both are BEFORE DELETE, so a row-local constraint could not express them and the sibling read makes them cross-row. Matches have no gate of their own — deliberately: a closed match inside a still-open round is deletable (the e2e case documents that exposure, ADR-007 "one level up"). Refusal UX defect: the handlers write the *referential* message ("the record is referenced by other records and cannot be deleted") for these two gates, which are about durability, not references; a client cannot tell "still in use" from "this is history". The season half composes with R-SCHEDULE-026/R-SCHEDULE-027: the season DELETE is the refuse point of an all-or-nothing transaction that has already deleted the season's players through game (migration 000021), so a gate refusal rolls the registrations back instead of leaving the season alive without them. Two refusals end that same tx: this gate's P0001 and game's rides_player_id_fkey 23503; both arrive as ErrRecordInUse (409).
