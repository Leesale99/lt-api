---
id: R-PLATFORM-001
title: Configuration is resolved flag over environment over default
statement: |
  A deployment's settings come from its environment, and an operator may override them
  per run with a flag; when neither says anything, a built-in default applies. Values
  that are deployment data rather than per-run operator choices — the cross-origin
  allowlist, the build version — are not overridable at all.
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
refusal: null
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

One entry point (ADR-014): the binary reduces to Parse(os.Args[1:]) plus wiring, and no other code reads os.Getenv or a flag. The flag set is private to the binary (no global CommandLine, ContinueOnError so a bad flag is an error, not an exit) — an unknown flag fails with the flag named. Authority note: composition-root, not the orchestration default (service) — resolving the configuration is process-level wiring done once at assembly (Parse is called from cmd/api/main.go), before any request exists and with no use case to own it. No refusal of its own: reading a setting cannot fail — a bad or missing value is R-PLATFORM-002/003's startup refusal, and this rule only fixes where a value comes from. Category note: `orchestration`, not `interface` — the rule orders the process's own intake (environment parsed before flags, defaults last), so it has no refusal status to name (policy 14); nothing on its sites sees a request, which is also why the layer is composition-root and not handler.
