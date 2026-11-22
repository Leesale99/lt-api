---
id: R-SCHEDULE-007
title: A closed match records when it ended
statement: |
  A match carries an end time if and only if it is closed, and that end is
  later than its start. An in-progress or scheduled match has no end, so a
  reader can never mistake a running match for a finished one.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000005_add_matches_ended_at.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/model/match.go::validateMatchShape
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23514"
  constraint:
    - matches_ended_at_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
  e2e: null
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses an end that does not fit the match
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

One biconditional CHECK: (status = 'closed') = (ended_at IS NOT NULL), plus ended_at > starts_at. ended_at is nullable by necessity (000005 could not add it NOT NULL: the column only exists on closed matches). Coverage gap: TestMatchConstraints exercises the *accepting* side (a closed insert must supply ended_at, since the stamping trigger only fires on UPDATE) but there is no negative case for "closed without ended_at" or "ended_at before starts_at", so the CHECK's refusal is not directly probed. Feed-completion semantics that the game context reads (NextForTeam's `ended_at IS NULL` filter, destination_open_guard's liveness test) all depend on this biconditional.
