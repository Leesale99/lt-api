---
id: R-PLATFORM-006
title: Mail cannot be sent from an incomplete configuration
statement: |
  If the outbound mail settings are incomplete — no host, no credentials, no sender —
  the process refuses to start instead of accepting requests it cannot honour. The
  refusal is distinguished from a delivery failure, so a configuration mistake is never
  retried as if it were a network problem.
module: platform
entity: email_delivery
category: orchestration
status: implemented
authority: composition-root
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/mailer/mailer.go::New
    layer: composition-root
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrMissingCredentials
  http: null
fixture: null
verification:
  unit:
    - internal/platform/mailer/mailer_test.go::TestNewRefusesIncompleteConfig
    - internal/platform/mailer/mailer_test.go::TestNewAcceptsCompleteConfig
    - internal/platform/mailer/mailer_test.go::TestSendFailureIsNotConfigError
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Refusal happens at construction, and the composition root builds the mailer *before* it dials any database, so a configuration error fails before a connection error (that ordering is main's, and it is what makes this a boot rule rather than a first-send surprise). The mailer suite now constructs the real adapter: it pins the boot refusal for each missing setting and pins that a delivery failure is not reported as a configuration error. This rule's check is non-empty only — deliberately: a *malformed* sender is a bound, so config's validate() judges it at boot (R-PLATFORM-003, item 16 of the misalignment report, gap closed: SMTP_PORT is bounded and SMTP_SENDER must parse as an RFC 5322 mailbox), and an empty one is refused twice (here and there) with this rule's sentinel being the mailer's own. Category note: `orchestration`, not `interface` — incompleteness is refused at construction, before the process serves, so the rule has no HTTP refusal to name (policy 14). Authority note: composition-root, not the orchestration default (service) — the mailer is built by the composition root before it dials a database, so the decision belongs to assembly and no use case participates.
