---
id: R-PLATFORM-002
title: Startup refuses a configuration it cannot honour, and names the setting
statement: |
  A configuration value that is missing, unparseable or out of its allowed range stops
  the process before it serves anything, and the message names the setting that must
  change — both its environment variable and its command-line flag, because the same
  value can arrive either way. Every bad environment value is reported in one refusal
  instead of one at a time, and nothing degrades into a silent default.
module: platform
entity: configuration
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/config/config.go::Parse
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
    - internal/platform/config/config_test.go::TestValidate
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The refusal is a startup error, not an HTTP status (hence http: null): the caller is the process, which logs and exits 1. Contract details the tests pin: env values are parsed and joined before flags ever parse, so all bad variables surface together; flag parse errors are ordinary errors, not exits; a returned error is always safe to log verbatim. Every semantic refusal names its setting by both spellings — `invalid PORT 0: must be in range 1-65535 (env PORT or -port flag)` — because the stdlib flag package exposes no "was this flag passed" for a typed value, so validate() cannot know which one supplied it. Naming both spellings is what stops a -port caller being sent to a variable they never set: TestValidate's wantErr/wantFlag pair and TestParse's "bad flag port names both spellings" case pin it. The SMTP variables are not validated here at all: R-PLATFORM-006 owns them, at construction and in config's own validate. Category note: `orchestration`, not `interface` — a startup refusal stops the process before it serves anything, so it has no HTTP status to name (policy 14); the refusal recorded here is the process's exit. Authority note: composition-root, not the orchestration default (service) — the refusal is decided where the process is assembled (Parse, called from cmd/api/main.go), before any use case exists.
