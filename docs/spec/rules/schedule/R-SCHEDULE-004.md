---
id: R-SCHEDULE-004
title: The status vocabularies are closed sets shared by code and database
statement: |
  Every season, round and match status is one of the values the league
  recognises, and the database accepts exactly the set the code knows. A
  status word nobody defined is unrepresentable, so no reader ever has to
  guess at an unknown state.
module: schedule
entity: season
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
  - site: internal/schedule/internal/model/season.go::ValidateSeasonStatus
    layer: validator
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/model/round.go::ValidateRoundStatus
    layer: validator
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/model/match.go::ValidateMatchStatus
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - seasons_status_check
    - rounds_status_check
    - matches_status_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestStatusVocabularyIsInSync
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::A status the league never defined cannot be stored
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Three CHECKs of one kind, one rule (per-constraint CHECKs of a kind are enforcement sites, not near-duplicate rules). The Go slices (SeasonStatuses, RoundStatuses, MatchStatuses) and the CHECK lists must agree — this is the one pair in the module tied by a live change detector: TestStatusVocabularyIsInSync inserts every Go value through the owner pool and fails if the database rejects one, so the two sides cannot drift silently. The validators are advisory copies that only improve the refusal (a lowercase-exact 422 instead of an unmapped 23514); values are matched exactly on the wire, with no normalisation. A 23514 from any of the three tables is unmapped in the schedule repositories, so a writer that bypasses the validators gets a 500 rather than a 4xx.
