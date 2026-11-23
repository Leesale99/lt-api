---
id: R-SCHEDULE-011
title: A match belongs to a round of its own season
statement: |
  A match's season and its round are the same season: the round it names must
  itself belong to the season it names, and both must exist. A calendar can
  therefore never hold a fixture that sits in one season while pointing at
  another season's round.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: composite-fk
write_scope: cross-table
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/handler/matches.go::createMatch
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23503"
  constraint:
    - matches_season_id_round_id_fkey
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
  e2e:
    - internal/e2e/matches_test.go::TestCreateMatchHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses a fixture moved into another season
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The mechanism is the pair rounds_season_id_id_key UNIQUE (season_id, id) + matches_season_id_round_id_fkey — a composite FK, i.e. cross-table consistency without procedural code. Season_id is denormalised onto matches deliberately (000001: "the whole season is one indexed lookup"), and this FK is what keeps the denormalised copy honest; matches_season_id_fkey and matches_round_id_fkey are the plain-FK halves of the same statement, and matches_home/away_team_id_fkey carry the team side. The handler's copy (round must belong to the season, both teams must exist -> 422) only changes the refusal shape and is advisory by definition. Mapping gap: 23503 is mapped to ErrRecordNotFound on match UPDATE only; at INSERT the repository returns it raw, so a race between the handler's existence checks and the insert answers 500.
