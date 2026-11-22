---
id: R-SCHEDULE-013
title: A round's number is one to thirty-eight
statement: |
  A round is numbered within the season's fixture list: at least one, at most
  thirty-eight — ten matches a round, twenty teams home and away. There is no
  representable round zero or round thirty-nine.
module: schedule
entity: round
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/model/round.go::ValidateRound
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - rounds_number_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestRoundAndSeasonConstraints
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
    - internal/e2e/rounds_test.go::TestCreateRoundHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses a round numbered outside the fixture list
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The bound is duplicated as calendar vocabulary in model.RoundsPerSeason = 38 (the same constant the close flow reads as "is this the season's last round"), so the CHECK and the constant must move together — nothing ties them; a change to one alone is silent (the validator/CHECK drift detector, TestStatusVocabularyIsInSync, covers statuses only, and number bounds are probed only as 0 and 39). RoundsPerSeason is also load-bearing for R-SCHEDULE-018 and for the game context's final-round settlement.
