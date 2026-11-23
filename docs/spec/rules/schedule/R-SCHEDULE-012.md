---
id: R-SCHEDULE-012
title: A schedule record never changes parents
statement: |
  A round stays in the season it was created in, and a match stays in the
  season and round it was created in. Re-parenting is not an edit; it is a
  different record.
module: schedule
entity: round
category: invariant
status: implemented
authority: repository
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.Update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/match_repo.go::MatchRepository.Update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal:
  code: "23503"
  constraint:
    - matches_season_id_round_id_fkey
  sentinel: null
  http: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
    - internal/e2e/matches_test.go::TestUpdateMatchHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  RoundRepository.Update's own comment states the wrong reason for the omission (it says re-parenting would break the composite FK for every match "in it"; that is the DB backstop, not the reason the column is absent).
tie: |-
  the DB copy fails loudly — a repository path that started writing season_id would trip matches_season_id_round_id_fkey (23503) for any round with matches.
---

## Notes

Authority deviates from the invariant default (database) on purpose: the guarantee is the *absence* of season_id/round_id from the two UPDATE statements plus the absence of the fields from the update DTOs — a path-dependent guarantee, not a predicate any constraint can state (a CHECK cannot see "unchanged", and the DTO is the wire half). The database is a partial backstop only: re-parenting a round that already owns matches violates the composite FK, but a round with no matches can be re-parented in SQL and no CHECK notices. Coverage gap: the two e2e pointers prove the field is rejected on the wire (`{"season_id":2}` is a 400 unknown key) but nothing pins the SQL omission itself — there is no call-set fixture, so a new update statement that included the parent would fail no test.
