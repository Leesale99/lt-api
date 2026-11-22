---
id: R-SCHEDULE-027
title: Deleting a season also deletes its players
statement: |
  Removing a season removes its players first, through the game context, and
  only then the season row. The result is the old cascade's: the season's
  players and its calendar go together, and a ride still hanging on one of
  those players still refuses the whole delete.
module: schedule
entity: season
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/service/delete_policy.go::Service.DeleteSeason
    layer: service
    role: authoritative
    pinned_by: null
refusal:
  code: "23503"
  constraint:
    - rides_player_id_fkey
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/seasons_test.go::TestDeleteSeasonHandler
  gherkin:
    - docs/spec/features/schedule/deletion.feature::A deleted season takes its registrations with it
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The shape is a replayed cascade: players.season_id -> seasons was ON DELETE CASCADE until 000016 dropped the cross-context FK, so the policy reproduces the row outcome by asking game to delete the season's players before the schedule delete. The refusal surface is inherited, not rebuilt: a ride still referencing a player trips rides_player_id_fkey (a game-internal FK that stays), game raises its own "in use", the adapter translates it, and the handler answers the same 409 a blocked cascade produced. Two recorded findings. (1) Verification gap: no test asserts that a season's players are actually deleted with it — TestDeleteSeasonHandler only checks the status code for the created-season case, and TestDeleteCreatedSeasonHandler counts seasons and rounds, not players. (2) Atomicity: the players are deleted before the season row, and both live in schedule's one transaction (migration 000021's definer function): a season whose delete is then refused (R-SCHEDULE-023's gate) rolls the player delete back with it, so the row outcome and the atomicity are both the old cascade's.
