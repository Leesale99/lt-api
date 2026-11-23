---
id: R-SCHEDULE-029
title: A team carries a name, a badge and a description
statement: |
  A team always has a name, a badge and a description, each non-empty, with the
  name and the description bounded in length. A half-described team is not a
  representable record.
module: schedule
entity: team
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
  - site: internal/schedule/internal/model/team.go::ValidateTeam
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - teams_name_check
    - teams_logo_check
    - teams_description_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestTeamConstraints
  e2e:
    - internal/e2e/teams_test.go::TestCreateTeamHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses a team with a part of its record missing
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Three CHECKs of one kind (000001 also sets NOT NULL on all three columns, so the emptiness CHECKs are the belt to the NOT NULL braces). The byte bounds are octet_length(name) <= 500 / octet_length(description) <= 5000 in SQL and len(...) on the Go string — both byte counts, so they agree; the boundary is pinned at exactly 500 on both sides. The badge's *content* is not constrained here (only non-empty); its URL shape is a separate, Go-only rule (R-SCHEDULE-030). The Go copy is advisory: it only turns an unmapped 23514 into a 422, and the team repository maps no CHECK violation, so a bypassing writer still gets a 500.
