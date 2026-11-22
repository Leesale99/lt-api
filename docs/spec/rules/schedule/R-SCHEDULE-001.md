---
id: R-SCHEDULE-001
title: A new schedule record is born in its created state
statement: |
  A season, a round and a match always come into existence in their created
  state. The client cannot choose it — the create request carries no status —
  so no organiser can plant a live or finished season, round or match with a
  single call.
module: schedule
entity: season
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/handler/seasons.go::createSeason
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/handler/rounds.go::createRound
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/handler/matches.go::createMatch
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/model/season.go::ValidateNewSeason
    layer: validator
    role: defense-in-depth
    pinned_by: null
  - site: internal/schedule/internal/model/round.go::ValidateNewRound
    layer: validator
    role: defense-in-depth
    pinned_by: null
  - site: internal/schedule/internal/model/match.go::ValidateNewMatch
    layer: validator
    role: defense-in-depth
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
    - internal/e2e/seasons_test.go::TestCreateSeasonHandler
    - internal/e2e/rounds_test.go::TestCreateRoundHandler
    - internal/e2e/matches_test.go::TestCreateMatchHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the three validators fail loudly at the transport — a create that carried any other status would be refused 422 before the use case, and the e2e create suites pin the responses.
---

## Notes

Three request DTOs, one rule: none of the create bodies has a status field (an attempt to send one is a 400 unknown key, the frozen wire contract), and each handler writes the created constant itself. The database is not a copy of this rule and could not be: no CHECK forces 'created' on INSERT, and rounds_progress_gate only constrains an insert that is already beyond created. A direct writer can therefore insert an open round under a live season with an unbroken sequence (the seed's vocabulary drift detector relies on exactly that door). For matches the create SQL does not even send the status column — the 000001 column DEFAULT 'created' is what stores it, which makes the handler's assignment for matches inert (it is the validator plus the DEFAULT that hold). The two new-record validators, ValidateNewSeason and ValidateNewRound, have no unit test today; only their match counterpart is pinned.
