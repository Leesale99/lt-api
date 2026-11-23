---
id: R-PLATFORM-009
title: Every connection is bounded in time
statement: |
  The HTTP server bounds how long it will spend on a connection: a read must finish
  within five seconds, a write within ten, and an idle connection is closed after a
  minute. A client that cannot meet those bounds does not hold a server resource
  indefinitely.
module: platform
entity: http_server
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/server/server.go::Server.Serve
    layer: composition-root
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/server/server_test.go::TestServeReadTimeoutClosesSilentConnection
    - internal/platform/server/server_test.go::TestServeWriteTimeoutClosesStreamingResponse
    - internal/platform/server/server_test.go::TestServeIdleTimeoutClosesKeepAlive
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The three values are written once, where the server is built, and are not configurable — a deployment cannot tune them. The server suite pins all three: a silent reader, a stalled streaming write and an idle keep-alive connection are each closed at their bound, so a removed or lengthened timeout fails a test. Refusal surface is null on purpose: exceeding a write deadline truncates the response and closes the connection; there is no named refusal. Category note: `orchestration`, not `interface` — the bounds are set once where the server is built and enforced by closing a connection instead of writing a named refusal, so the interface category's status requirement cannot hold (policy 14). Authority note: composition-root, not the orchestration default (service) — Serve is the assembly point that builds the http.Server with the consts.go bounds (tests reach the same mechanism through serveWithOptions), so no request and no use case decides them.
