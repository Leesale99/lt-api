---
id: R-SCHEDULE-015
title: A round only goes live under a live season with an unbroken sequence
statement: |
  A round may only become open or closed while its season is open or in
  progress, and only when every earlier round of that season is already
  closed: the highest closed round must be exactly the predecessor. The season
  can never be one round ahead of its own fixture list, which is what makes
  the last-round flip sound.
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
  - site: migrations/000019_add_status_rank_gates.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - round_progress_season_not_live
    - round_sequence_incomplete
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/service/rounds_close_test.go::TestRoundClose_MidTxFailureLeavesNoTrace
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::The database refuses a round opened behind an unclosed predecessor
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  round_repo.go::updateRound's IsTriggerViolation comment names only rounds_freeze_gate as the cause of the P0001 it maps to ErrRecordInUse, but the same SQLSTATE also comes from rounds_progress_gate (000008) and rounds_close_gate; the mapping is by SQLSTATE, not by raise name, so the comment's list of causes is one short (and the handler's frozen-hierarchy message is what makes the gap visible).
tie: null
---

## Notes

rounds_progress_gate fires on INSERT beyond created, on every forward transition (created -> open/closed, open -> closed) and on any renumbering of a *live or closed* round (OLD.number IS DISTINCT FROM NEW.number, inside the NEW.status IN ('open','closed') branch), which is what stops a writer from renaming its way around the order; a created round renumbers freely, subject only to the per-season unique number. Both halves read sibling rows (seasons, sibling rounds), so a trigger. Two migration sites, because the function's current body is 000019's: 000008 created it, and 000019 gave the same function R-SCHEDULE-003's rank half — round_status_regression — checked before this gate's own two, so the rank message wins when both could fire (that name belongs to R-SCHEDULE-003's refusal list, not this one's). Two findings. (1) Verification gap: the season-live half is pinned by the close test, the *sequence* half has no direct test anywhere — the two schedule suites instead disable the trigger to build fixtures ("rounds_progress_gate requires an unbroken closed sequence, which no realistic fixture builds"), so the rule's central half is unpinned and the escape hatch is the only evidence it exists. (2) Refusal UX defect: every round-update refusal maps to ErrRecordInUse and the handler answers the *frozen* message ("a match has already started, so the hierarchy cannot move to an earlier stage"). A round going live under a created or closed season trips round_progress_season_not_live, and a close over unclosed matches trips rounds_close_gate, both with no started match anywhere, and both still get that message; those causes are indistinguishable to a client. A created round in a created season can be renumbered — the gate's renumber branch requires NEW.status IN ('open','closed') — so only the per-season unique number can refuse it.
