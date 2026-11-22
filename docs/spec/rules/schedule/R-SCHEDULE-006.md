---
id: R-SCHEDULE-006
title: A match carries a score exactly while it is playing or finished
statement: |
  A match has either no score at all or both halves of one, and never a
  negative number. Scores exist only from the moment the match is in progress
  or closed; a created or postponed match carries none.
module: schedule
entity: match
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
  - site: internal/schedule/internal/model/match.go::validateMatchShape
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - matches_status_score_check
    - matches_score_complete_check
    - matches_home_score_check
    - matches_away_score_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateMatchUpdate
  e2e: null
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses a score that does not fit the match
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Four CHECKs, one statement: the score/status matrix and the three bounds are the same kind of fact, so they are catalogued together with the constraint names kept individually mappable. matches_status_score_check states the one-sided matrix (in_progress/closed REQUIRE home_score, created/postponed FORBID it) and matches_score_complete_check makes the pair atomic — together they are why Match.Winner needs no nil guard on a closed match. The Go copy runs in validateMatchShape for both create and update and only exists to turn a would-be 500 into a clean 422. Same gap as the other match CHECKs: nothing maps 23514 to a sentinel, so a direct writer's refusal is a 500.
