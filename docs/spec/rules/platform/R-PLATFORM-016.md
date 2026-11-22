---
id: R-PLATFORM-016
title: A failing background pass never kills the process
statement: |
  A background pass that fails is logged and retried later: the failure never takes the
  process down and never stops the next pass from running.
module: platform
entity: worker
category: orchestration
status: specified-only
authority: composition-root
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

Specified only. Phase 07 §"Start with one worker" (the loop logs and continues: `slog.Error("auto burn failed", ...)` with no return) and task 4 "handle worker errors without killing the process". No worker exists, so there is nothing to test. The process-level host that would carry it does exist — internal/platform/server (Background(fn) tracked on the same waitgroup Serve drains, RootCtx canceled on SIGTERM/SIGINT) — but no domain job registers on it. Authority is the composition root: the loop and its error containment live in the wiring (main), not in a module. Numbered from the phase-doc sweep (was provisional R-PLATFORM-201).
