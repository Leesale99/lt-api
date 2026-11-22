---
id: R-PLATFORM-017
title: A background worker stops on cancellation and is waited for
statement: |
  A background worker runs under the process's cancellation context and is waited for at
  shutdown: on a signal it stops taking new work, the process waits for what it already
  started, and only then exits.
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

Specified only as a rule about workers. Phase 07 §"Graceful shutdown" shows the shape — signal.NotifyContext, a WaitGroup, stop then wg.Wait() — and tasks 5-6 ask for context cancellation plus graceful-shutdown or focused lifecycle tests; avoiding duplicate work and the "long-running process lifecycle" lessons are the same subject. The generic machinery is built for HTTP handlers (internal/platform/server: RootCtx, bounded drain, second-signal escalation, waitgroup for Background tasks) but no worker is started on it, so the rule has no subject yet. Kept separate from R-PLATFORM-016 because the two are independently falsifiable: a worker can survive errors while failing to drain. Numbered from the phase-doc sweep (was provisional R-PLATFORM-202).
