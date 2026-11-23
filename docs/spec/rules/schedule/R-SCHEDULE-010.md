---
id: R-SCHEDULE-010
title: A team never plays itself
statement: |
  The two sides of a match are two different teams. A calendar entry with the
  same club on both sides is not a fixture and cannot be stored.
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
    - matches_teams_differ_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
  e2e: null
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The organiser cannot announce a team against itself
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

A one-line row-local CHECK, mirrored in validateMatchShape for a clean 422. The app copy is not unit-tested (TestValidateNewMatch has no same-team case), so only the DB probe covers the rule. Unmapped 23514 as everywhere on matches: a bypassing writer gets 500.
