---
id: R-PLATFORM-005
title: The cross-origin allowlist holds bare origins, never a wildcard
statement: |
  The deployment lists the origins it serves cross-origin, one bare origin each; a
  wildcard is refused at startup rather than degraded into "allow everything". Origins
  are normalized to lowercase scheme and host with any trailing slash removed, a value
  carrying a path, query or fragment is refused as not an origin, duplicates collapse,
  and an empty list means cross-origin access is simply off.
module: platform
entity: cors_origin
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/config/config.go::parseCORSOrigins
    layer: composition-root
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/platform/config/config_test.go::TestParseCORSOrigins
    - internal/platform/config/config_test.go::TestParse
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The half of CORS the deployment owns (config.Parse is the only caller and wraps any failure as "invalid CORS_ORIGINS"; there is deliberately no flag for this value — an allowlist is deployment data, not an operator override). The matching half is R-KERNEL-010, which compares the canonicalized strings exactly. Normalization is what makes that exact comparison correct (schemes and hosts are case-insensitive in URLs), so the two rules are paired by the string form they agree on — a shared table of vectors would be the ideal fixture and does not exist; the config suite pins the normalization and the e2e CORS suite pins exact matching from the other end. Empty (or whitespace/comma only) yields nil, which the middleware reads as "no origin is allowed". Category note: `orchestration`, not `interface` — the allowlist is normalized and refused at startup, so there is no request to frame and no HTTP refusal to name (policy 14). Authority note: composition-root, not the orchestration default (service) — config.Parse is its only caller and runs while the process is assembled, before any use case exists.
