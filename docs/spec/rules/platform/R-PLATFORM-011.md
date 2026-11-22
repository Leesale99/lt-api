---
id: R-PLATFORM-011
title: Background work may fail without taking the process down
statement: |
  Work that runs after a response has been sent — a mail delivery, a background sweep —
  is tracked by the server and runs under the process's cancellation root. If it fails
  or panics, the failure is recorded and the process keeps serving: once a response has
  been committed, no downstream task may kill it.
module: platform
entity: process_lifecycle
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/server/server.go::Server.Background
    layer: service
    role: authoritative
    pinned_by: null
  - site: cmd/api/main.go::main
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/server/server_test.go::TestBackgroundPanicIsContainedAndRecorded
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-07-background-workers.md
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (policy → domain): the decision is a lifecycle mechanic owned by the process host, with no domain participant. The composition root is the other site because booking is explicit there — the identity module receives the tracker and the rate-limiter sweep is booked before serving starts, so the drain cannot miss it. The panicking task is logged with its value and no stack. The server suite now pins the containment: a panicking background task is recorded and the process keeps serving, and the wait budget keeps its own test. No refusal: containment is about surviving a failure, not refusing a request.
