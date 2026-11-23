---
id: R-SCHEDULE-019
title: A round close announces its results and settles nothing itself
statement: |
  Finishing a round commits the round and then announces the round's complete
  result set to the rest of the system. The close itself writes no ride and no
  player: settling what was riding on the round is the game context's own,
  later transaction, so undecided rides never block a close and a closed round
  may still carry them.
module: schedule
entity: round
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/service/round_lifecycle.go::RoundService.Close
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/match_repo.go::MatchRepository.ResultsForRound
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/service/rounds_close_test.go::TestRoundClose
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the repository copy fails loudly enough to be noticed — it filters status = 'closed' so a results read can never carry an unfinished match, and the close gate (R-SCHEDULE-016) refuses a close over unclosed matches, so a wrong read is refused one step earlier.
---

## Notes

The transaction-to-event ordering is the rule: the repository commits the flip, the service reads the results and publishes RoundCompleted (post-commit, in-process, synchronous) with a fat payload and an EventID the subscriber claims. No refusal of its own: a failed publish is logged and dropped (the accepted crash gap, swept in Phase 07), and the request still succeeds because its write is durable; undecided-ride refusal was removed by 000014 and is now the designed intermediate state (pinned by TestRoundClose's "close over undecided rides commits — settlement is eventual" subtest). Migration 000014's header is accurate about this; the ride-side settlement rules are game's (R-GAME-009).
