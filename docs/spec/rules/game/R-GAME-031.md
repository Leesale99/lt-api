---
id: R-GAME-031
title: A ride past its deadline is burned by the system
statement: |
  A ride whose decision window has expired is burned by the system itself, with no
  player command: a background worker picks up the due rides on its own schedule and
  applies the auto-burn.
module: game
entity: ride
category: policy
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
knowledge-ref: docs/league-tokens-learning-phases/domain-knowledge-game-engine.md
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 07 §"Start with one worker" gives the shape — game.ProcessDueAutoBurns(ctx, now) called from a ticker loop — and §"Done when" the promise: "An expired ride can be automatically burned without a user request"; task 7 adds a persisted deadline "if the in-memory schedule is insufficient". Not in code: no worker package under internal/ (only internal/game, identity, kernel, ledger, platform, schedule, testdb, arch, e2e, testdb), no ticker or deadline anywhere outside the HTTP rate limiter, rides carry no decision-deadline column (migrations/000006), and no ProcessDueAutoBurns exists. Authority is the service, deviating from the policy default deliberately (same reasoning as R-GAME-009, which must appear in notes per the schema): the operation is system-initiated, so the domain's per-command phase guards (R-GAME-007) do not apply. Nearest implemented analogue: the season-end force-burn triggered by RoundCompleted (R-GAME-009) — round-close-triggered, not deadline-triggered.
