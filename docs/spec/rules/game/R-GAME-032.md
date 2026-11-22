---
id: R-GAME-032
title: The auto-burn is safe to run twice
statement: |
  Running the auto-burn more than once — two ticks, a restart, two processes — never
  burns or rewards twice: the operation recognises state it has already processed and
  does nothing.
module: game
entity: ride
category: orchestration
status: specified-only
authority: service
mechanism: null
write_scope: null
enforcement: []
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-07-background-workers.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 07 §"Worker safety" never trusts the worker to run exactly once ("worker restarts -> operation sees already-processed state -> does nothing"), §"New concept" names avoiding duplicate work as one of the four lessons, and §"Done when" adds that a restart must not cause duplicate rewards or corrupted state; the phase explicitly reuses phase-04's idempotency. Nothing exists because the worker does not exist. The ingredients the phase assumes are present — the ride state gate freezes terminal rides (R-GAME-002/003) and kernel/idempotency can claim an id before the write — but no auto-burn path calls them. Numbered from the phase-doc sweep (was provisional R-GAME-205).
