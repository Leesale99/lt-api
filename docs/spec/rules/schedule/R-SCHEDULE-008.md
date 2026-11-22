---
id: R-SCHEDULE-008
title: The end of a match is stamped once by the database
statement: |
  The end time of a match is written once, on the single transition into
  closed, and never rewritten afterwards. The resolve flow supplies the match's
  actual end time — a client fact the time policy (R-SCHEDULE-020) validates —
  and the database keeps it; when a writer supplies no end time, the database
  stamps its own clock. Editing a finished match cannot move its end time.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: cross-row
enforcement:
  - site: migrations/000005_add_matches_ended_at.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
  - site: migrations/000020_respect_supplied_match_ended_at.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchHandler
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The end the organiser resolved with is the end the calendar keeps
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

matches_set_ended_at_on_close assigns NEW.ended_at := now() when NEW.status becomes closed and OLD.status was not — OLD-vs-NEW, so a trigger, and INSERT-only tails are out of its reach. Its current body is 000020's (two sites, 000014's pattern: 000005 created the function, 000020 replaced the body). **Resolved (Phase G, §6 item #1):** the trigger keeps a supplied NEW.ended_at on the transition into closed and stamps now() only when it is NULL (000020's body), so the committed row holds the validated value rather than the database's clock. ResolveTx returns the row's ended_at through RETURNING instead of a pre-seeded request copy, and the HTTP 200 plus the post-commit MatchResultRecorded echo the row's values. A direct writer that closes without an end time still gets the database's now() backstop, which is the half this rule keeps for writers that do not know the fact. The old discrepancy is fixed and pinned end to end: TestResolveMatchHandler asserts the echoed value AND reads the committed row back to it. Verification gap: the stamp-once half is unchanged — nothing probes a second edit of a closed match. No refusal of its own: the stamp is a write the database performs, not a predicate, so nothing is refused and the field stays null — the refusals around it belong to R-SCHEDULE-003 (closed is final) and R-SCHEDULE-021 (the guarded resolve).
