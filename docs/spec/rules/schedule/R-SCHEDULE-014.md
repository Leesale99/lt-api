---
id: R-SCHEDULE-014
title: A season has one round per number
statement: |
  Within a season each round number is used once. Two rounds may share a
  number only if they belong to different seasons, so a season's fixture list
  has no duplicates.
module: schedule
entity: round
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: cross-row
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.Insert
    layer: repository
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.updateRound
    layer: repository
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23505"
  constraint:
    - rounds_season_id_number_key
  sentinel: ErrDuplicateRecord
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestRoundAndSeasonConstraints
  e2e:
    - internal/e2e/rounds_test.go::TestCreateRoundDuplicateNumber
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The organiser cannot add a second round 1
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The uniqueness is scoped to the season (UNIQUE (season_id, number)), which is the whole point: season 2's round 1 does not collide with season 1's. Both repository paths map 23505 to ErrDuplicateRecord and the handlers answer 409 "a record with these unique values already exists" — the mapping is advisory in the copy-policy sense (it changes the refusal shape, the constraint still refuses). Note the interaction with R-SCHEDULE-015: because renumbering a live or closed round is gated by the sequence, "renumber into a free number" is largely unreachable, so most duplicate-number attempts are reached through the gate first (the e2e cases rely on that ordering).
