---
id: R-SCHEDULE-022
title: A round's gameplay phase follows its match times
statement: |
  A round is in its action phase until one hour before its first match
  starts, in its match phase from then until one hour after its last match
  ends, and in its decision phase afterwards. A round with no ended match yet
  can never be in the decision phase, so the decision window cannot open on a
  match that is still running.
module: schedule
entity: round
category: policy
status: implemented
authority: domain
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/model/round_phase.go::Phase
    layer: domain
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/model/round_phase_test.go::TestPhase
  e2e: null
  gherkin:
    - docs/spec/features/schedule/resolve.feature::A lock landing exactly an hour before kickoff is refused
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

A pure derivation from two real-world facts (earliest start, latest end of the round's matches) — no state of its own, so nothing can drift and nothing refuses. The boundary convention is a contract, not an implementation detail: an instant landing exactly on a phase edge belongs to the later phase, and the test locks that in with nanosecond cases on both edges. The facts come from MatchRepository.PhaseForMatch (one statement: round status plus min(starts_at)/max(ended_at), grouped by round id), which is a read, not an enforcement site; the nil-ended-at rule is what keeps a running round out of the decision window. This rule gates nothing by itself: the commands each phase allows and their refusal live in game (R-GAME-007), which is the catalogued direction of that dependency.
