---
id: R-SCHEDULE-028
title: An update only lands on the version the caller was shown
statement: |
  Every edit is conditional on the version the client last saw. If the record
  moved in between, the write is refused as a conflict instead of silently
  overwriting the other editor's change.
module: schedule
entity: season
category: orchestration
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/repository/season_repo.go::SeasonRepository.Update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.updateRound
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/match_repo.go::MatchRepository.Update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/team_repo.go::TeamRepository.Update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/handler/rounds.go::updateRound
    layer: handler
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/handler/matches.go::updateMatch
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrEditConflict
  http: 409
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
    - internal/e2e/matches_test.go::TestUpdateMatchHandler
    - internal/e2e/teams_test.go::TestUpdateTeamHandler
    - internal/e2e/seasons_test.go::TestUpdateSeasonHandler
  gherkin:
    - docs/spec/features/schedule/deletion.feature::An edit written against an outdated reading is refused
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the orchestration default (service) because the guarantee is the `AND version = $n` clause inside each UPDATE and the 0-row read that follows it: path-dependent, so a new update statement without the clause would bypass it silently and no fixture pins the call set. Four disjoint cases (one statement per entity). The handler half is advisory in two senses: it refuses a mismatching X-Expected-Version *before parsing the body* (the documented wire contract, so a bad body cannot outrank a stale version), and RoundService.UpdateRound re-verifies the header because a use case must not trust that its caller checked. The version bump itself (version = version + 1, store-stamped) is a persistence fact, not a business decision, and is not catalogued separately. No unit test exists for the CAS itself — every pointer is an e2e case.
