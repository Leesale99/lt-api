---
id: R-PLATFORM-004
title: A log level is an enumerated name, never a silent fallback
statement: |
  The logging level is one of the four names the deployment documents — debug, info,
  warn, error — matched without regard to case. An unrecognized level stops startup and
  the message names where the bad value came from, because a fallback would either
  flood or silence a deployment whose level was mistyped.
module: platform
entity: configuration
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/config/config.go::parseLogLevel
    layer: composition-root
    role: authoritative
    pinned_by: null
  - site: internal/platform/config/config.go::readEnv
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
    - internal/platform/config/config_test.go::TestParse
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Kept separate from R-PLATFORM-003 because the refusal carries an extra obligation: the source is named ("LOG_LEVEL" from the environment, "-log-level" from args), and the environment value is validated before flags parse so a failure at that point can only be the flag's. The parser itself is the standard library's — numeric offsets such as INFO+2 are therefore accepted even though the message advertises four names. Category note: `orchestration`, not `interface` — an unknown level is a startup refusal, not a framed request (policy 14); no HTTP status is written. Authority note: composition-root, not the orchestration default (service) — the level is parsed once, while Parse assembles the configuration, so no use case is in the loop.
