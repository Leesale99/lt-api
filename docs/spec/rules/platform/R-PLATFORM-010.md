---
id: R-PLATFORM-010
title: Shutdown is bounded and never hangs
statement: |
  When the process is asked to stop, it stops accepting work, gives in-flight requests a
  fixed grace period to finish, and force-closes whatever is left when that period
  expires. A second stop request during the drain closes connections immediately, and
  after the drain the process waits a bounded time for background work before exiting.
  A drain that had to be forced is still a successful stop, not a failure.
module: platform
entity: process_lifecycle
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/server/server.go::Server.runServer
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/platform/server/server.go::Server.shutdown
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/platform/server/consts.go::ShutdownGracePeriod
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/platform/server/consts.go::BackgroundTaskBudget
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/server/shutdown_test.go::TestDrainTimeoutForceCloses
    - internal/platform/server/shutdown_test.go::TestSecondSignalForceClosesDuringDrain
    - internal/platform/server/shutdown_test.go::TestBackgroundTaskBudgetBoundsWait
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-07-background-workers.md
stale_pointer: null
tie: null
---

## Notes

Budgets: ten seconds to drain, five for background work — deliberately nested so the worst case stays inside a typical orchestrator kill window. The budgets are options (zero value = the package defaults) only so tests can shrink them; the composition root passes none. The escalation is asserted, not assumed: a second signal during the drain force-closes, a handler that never finishes cannot hold the process, and a timeout on the background wait is logged and abandoned rather than returned as an error. Recorded gap for the report: nothing asserts the shutdown path end to end from cmd/api (the signal→root-context wiring, the pool closes), only the server's own lifecycle. No refusal: nothing is refused at shutdown — a drain that had to be forced is still a successful stop.
