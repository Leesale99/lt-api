---
id: R-SCHEDULE-016
title: A round closes only when every match in it is closed
statement: |
  A round may not be declared finished while any of its matches is still
  scheduled, running or postponed. Closing is the statement that the round's
  schedule is history, so every fixture in it must already have a result.
module: schedule
entity: round
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: cross-row
enforcement:
  - site: migrations/000008_add_round_lifecycle_gates.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
  - site: migrations/000014_relax_rounds_close_gate.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - round_close_has_unclosed_matches
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/service/rounds_close_test.go::TestRoundClose
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::The database refuses a close over a match still running
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

rounds_close_gate fires only on a transition INTO closed (a state-preserving rewrite of an already-closed round passes), then asks whether any match of the round is not closed. Two migration sites because the function body lives at its latest definition: 000008 created it and 000014 replaced it to drop the *other* check (undecided won_pending rides), so the current body is 000014's and the unclosed-matches half is byte-identical. The rule is a genuine DB backstop: the repository's close path does not settle matches, so the only way a round closes is that its matches were resolved first, by any writer. The replay half of the ride story is in R-SCHEDULE-019 (close does not refuse over undecided rides — a round with undecided rides closes fine). stale-pointer resolved: 000008's header claimed regressions "are NOT gated here", which was true only for this function's sibling (rounds_block_illegal_progress); that function gained its third definition in 000019 (R-SCHEDULE-003's rank half), while this rule's rounds_close_gate is byte-identical since 000014, so the overstatement is historical, not current; 000008 stays unedited as an applied migration.
