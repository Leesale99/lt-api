---
id: R-SCHEDULE-020
title: A match may only be resolved after it has ended
statement: |
  A result is accepted only with an end time that lies in the past. Resolving
  a match with an end that has not happened yet is refused before anything is
  written, so no result can be recorded from the future.
module: schedule
entity: match
category: policy
status: implemented
authority: service
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/handler/matches.go::resolveMatch
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrEndedAtInFuture
  http: 422
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchHandler
  gherkin:
    - docs/spec/features/schedule/resolve.feature::A result ending in the future is refused
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the policy default (domain) deliberately: the rule is checked at the *use-case door*, before any transaction is opened, so every caller — not only the HTTP handler — passes the same test, and so a legality answer can never be outranked by a storage conflict (a closed match would otherwise turn this into a 409 first). The validator (model.ValidateResolveMatch) deliberately does NOT check the clock: it is request shape only, field presence and score arithmetic, and says so at the site. Boundary: strictly before now, so ended_at == now is refused. The handler's copy is advisory — it turns the sentinel into the same 422 body shape the validator produces, which is the pre-split wire contract. No unit test covers the service door (the service test suite is close-only); the e2e case "ended_at in the future" is the only pointer. See R-SCHEDULE-008: the value this rule validates is the one the ended_at trigger keeps on the resolve transition (000020 respects a supplied end), so the policy guards a number that is actually stored and echoed.
