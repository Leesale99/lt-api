---
id: R-PLATFORM-008
title: One structured log stream per process
statement: |
  The process emits one machine-readable log stream on standard output at the level the
  deployment chose, and everything that logs — the application and the database driver
  — writes into that same stream. Call-site source locations are attached in
  development only, so production records carry no path bulk.
module: platform
entity: log_stream
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/logging/logging.go::New
    layer: composition-root
    role: authoritative
    pinned_by: null
  - site: internal/platform/logging/logging.go::newLogger
    layer: composition-root
    role: authoritative
    pinned_by: null
  - site: internal/platform/logging/logging.go::Pgx
    layer: composition-root
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/logging/logging_test.go::TestNewJSONOutput
    - internal/platform/logging/logging_test.go::TestNewLevelFiltering
    - internal/platform/logging/logging_test.go::TestNewSourceShortFileInDevelopment
    - internal/platform/logging/logging_test.go::TestPgxLevelMapping
    - internal/platform/logging/logging_test.go::TestPgxDataBecomesSortedAttributes
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The adapter is the interesting half: the database driver logs through the same handler at warn, with sorted attributes (map order would make two records of one event non-comparable) and the elapsed-time key renamed to elapsed_ns so it cannot collide with the JSON handler's own timestamp field — a record with two "time" keys is ambiguous to any parser. Source paths are shortened to <dir>/<file>:<line> in development, where a human reads them. No refusal: a log stream has nothing to refuse — the failures it records are other rules' refusals. Category note: `orchestration`, not `interface` — one stream for the whole process is startup wiring, not request framing, and it refuses nothing (policy 14). Authority note: composition-root, not the orchestration default (service) — New and Pgx are called where the process is wired, and no use case chooses the stream or its format.
