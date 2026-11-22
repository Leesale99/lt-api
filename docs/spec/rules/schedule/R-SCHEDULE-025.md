---
id: R-SCHEDULE-025
title: A team kept in the calendar by its matches cannot be deleted
statement: |
  A team that any match refers to cannot be removed. Match history is durable,
  so the calendar's teams are protected by the fixtures that name them, not by
  a check the application remembers to run.
module: schedule
entity: team
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: cross-table
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/team_repo.go::TeamRepository.Delete
    layer: repository
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23001"
  constraint:
    - matches_home_team_id_fkey
    - matches_away_team_id_fkey
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestTeamDeleteRestricted
  e2e:
    - internal/e2e/teams_test.go::TestDeleteTeamHandler
    - internal/e2e/teams_test.go::TestDeleteUnreferencedTeamHandler
  gherkin:
    - docs/spec/features/schedule/deletion.feature::A team a fixture names cannot be deleted
    - docs/spec/features/schedule/deletion.feature::The database refuses to delete a team a fixture names
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  TeamRepository.Delete's comment still says "teams is referenced with ON DELETE RESTRICT by matches and players" — the players half was dropped by migration 000016 (cross-context FKs), so only matches remain; the comment's rule is right, its list is one item too long.
tie: null
---

## Notes

ON DELETE RESTRICT (not NO ACTION) on both sides of matches, with the intent spelled out in 000001: "we don't want to delete matches history if we delete a team". The repository's copy maps 23001/23503 to ErrRecordInUse so the handler can answer 409 instead of a raw FK 500 — advisory, the FK refuses either way. Scope note: after the context split this is only the *schedule* half of "a team still in use"; the players and rides references moved to the composed delete policies (R-SCHEDULE-026).
