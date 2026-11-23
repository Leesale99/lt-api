---
id: R-SCHEDULE-030
title: A team badge is an image link
statement: |
  A team's badge is a web address ending in png, jpg, jpeg, svg or webp. A
  calendar cannot carry a badge nobody can display, so anything else is refused
  when the team is created or edited.
module: schedule
entity: team
category: interface
status: implemented
authority: domain
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/model/team.go::ValidateTeam
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/model/team.go::validImageURL
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
  unit: []
  e2e:
    - internal/e2e/teams_test.go::TestCreateTeamHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the interface default (handler) because the decision is a pure function in the model package that the transport merely runs: the handler has no rule of its own here, it turns the validator's field errors into the 422 envelope. validImageURL is listed as its own site because it is the reusable predicate (scheme http/https, a host, and an extension from the frozen set, case-insensitive) and a change confined to it would otherwise not show up in a diff of ValidateTeam. The rule is Go-only by design — the database constrains the badge to be non-empty and says nothing about its shape — so a direct writer can store any string, and the badge-shape claim is therefore a statement about what the API accepts, not about what the table can hold. No unit test: the only coverage is the e2e create suite's two cases ("logo not a URL", "logo wrong extension"); the update path is unpinned.
