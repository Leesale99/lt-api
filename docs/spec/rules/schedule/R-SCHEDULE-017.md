---
id: R-SCHEDULE-017
title: Opening a round starts its season
statement: |
  The moment a round is opened, its season starts running: the round's flip
  from created to open and the season's flip to in progress commit together or
  not at all. A live round therefore never sits under a season that has not
  started.
module: schedule
entity: round
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/service/round_lifecycle.go::RoundService.UpdateRound
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/round_repo.go::RoundRepository.Open
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: P0001
  constraint: null
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/rounds_test.go::TestOpenRoundStartsSeason
    - internal/e2e/rounds_test.go::TestOpenRoundSeasonFlipIsIdempotent
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::Opening round 1 starts the season
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the repository copy fails loudly — a dispatch that bypassed Open makes TestOpenRoundStartsSeason fail (the season would read 'open' instead of 'in_progress' at version 3), so the pair cannot drift silently.
---

## Notes

The decision is split by nature and the catalog gives it to the service: the created -> open transition must be *dispatched* to Open rather than to the plain update (a PATCH that went through RoundRepository.Update would flip the round and leave the season open), and that dispatch is RoundService.UpdateRound's switch. The two writes then ride one transaction inside RoundRepository.Open, whose season statement is conditional on status = 'open' so it is idempotent. Nothing in the database can refuse the wrong order: a round opening under a merely *open* (not in_progress) season satisfies rounds_progress_gate, so this is not a DB invariant. No unit test exists for the service-level dispatch; the two e2e pointers are the whole coverage.
