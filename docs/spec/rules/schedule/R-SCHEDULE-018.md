---
id: R-SCHEDULE-018
title: Closing the season's last round closes the season
statement: |
  Closing round thirty-eight ends the season in the same transaction. That one
  transition is the season's own last step, so a completed fixture list and a
  finished season can never disagree.
module: schedule
entity: season
category: orchestration
status: implemented
authority: repository
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.Close
    layer: repository
    role: authoritative
    pinned_by: null
refusal:
  code: P0001
  constraint: null
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/service/rounds_close_test.go::TestRoundClose
  e2e: null
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::Closing round 38 closes the season
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the orchestration default (service) because both the last-round predicate (round.Number == model.RoundsPerSeason) and the atomic pair live in Close: the round flip runs first, then the conditional season flip (WHERE status = 'in_progress', so it no-ops for rounds 1-37 and for a season that is not live). The statement order is load-bearing and documented at the site: the season must still be live while its round closes, or rounds_progress_gate (R-SCHEDULE-015) refuses the round flip — the mirror of Open, which flips the season before the round. The same predicate is re-derived in the service as the RoundCompleted payload's IsFinal (R-SCHEDULE-019), so the two halves of "final round" are stated twice: a change to RoundsPerSeason moves both, but nothing ties them. The "last round closes the season" path is only reachable in tests with rounds_progress_gate disabled (the fixture cannot build 37 closed rounds).
