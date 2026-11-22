---
id: R-SCHEDULE-002
title: A fixture is scheduled forward in time
statement: |
  A match may only be created for a start that lies strictly in the future.
  Calendar entries are announcements, so a past or exactly-now start is
  refused at creation; editing an existing match may still adjust a start
  that has already passed.
module: schedule
entity: match
category: policy
status: implemented
authority: domain
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/model/match.go::ValidateNewMatch
    layer: validator
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 422
fixture: null
verification:
  unit:
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch
  e2e:
    - internal/e2e/matches_test.go::TestCreateMatchHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::A fixture cannot be announced for a kickoff that has arrived
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The clock is injected (ValidateNewMatch takes `now`) so the boundary is testable without sleeping; the three cases — past, exactly now, one second after — are pinned. The asymmetry with update is deliberate and is stated in ValidateMatchUpdate: an existing match keeps being editable after it has started. Nothing in the database sees this rule (matches.starts_at has no CHECK against now()), so it is a domain policy for every creator, not a storage invariant.
