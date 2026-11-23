---
id: R-PLATFORM-003
title: Every setting has a bound, and the bound holds when the feature is off
statement: |
  Each setting is either usable or refused: the listen port and the mail port are real
  ports, the outbound sender is an address mail can be sent from, the environment is one
  of the three the deployment knows, a database connection string is present and
  parseable, the pool is at least one connection with a positive idle limit, and the
  throttle rates are positive. A setting is judged on its own merits — a throttle that
  is switched off must still be configured with usable values, because config that is
  only correct while something else is off fails unpredictably when it is switched on.
module: platform
entity: configuration
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/config/config.go::validate
    layer: composition-root
    role: authoritative
    pinned_by: null
  - site: internal/platform/config/config.go::parseLogLevel
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
    - internal/platform/config/config_test.go::TestValidate
    - internal/platform/config/config_test.go::TestParse
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Judgment calls worth naming: the database URL is validated by pgx's own parser, not by hand ("pgx is the authority on what a DSN is"), and an empty URL is refused explicitly because ParseConfig("") would fall back to libpq environment defaults and fail later as a connection error instead of a config error; the per-context URL overrides must each parse when present. The mail settings are judged here too (gap closed): SMTP_PORT carries the listen port's 1-65535 bound, and SMTP_SENDER is parsed as an RFC 5322 mailbox — net/mail, not kernel/validator's EmailRX, because the sender may carry a display name (the built-in default is "League Tokens <no-reply@lt.aleksrdvn.com>") and EmailRX is the account-address rule identity's registration uses. TestValidate's smtp cases pin the refusals (port below/above range, a negative port, a sender with no address, an empty sender) and the two accepted sender forms. A malformed sender therefore fails at boot instead of at the first delivery (R-PLATFORM-006's refusal is still the mailer's — config judges the value, the mailer refuses an incomplete set). A per-context override that names a *database* the context's role cannot see is not detectable here; that failure is deferred to the pool ping at boot. Category note: `orchestration`, not `interface` — the bounds are judged at startup, before a request exists, and a violation is a boot failure rather than an HTTP refusal (policy 14). Authority note: composition-root, not the orchestration default (service) — validate and parseLogLevel are called by Parse while the process is assembled, so the decision belongs to the wiring, not to any request's use case.
