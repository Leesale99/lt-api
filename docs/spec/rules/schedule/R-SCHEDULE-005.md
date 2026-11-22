---
id: R-SCHEDULE-005
title: A started match freezes the whole hierarchy
statement: |
  Once any match has started, nothing above it may move backwards: the match
  cannot return to created and cannot be postponed, its round cannot return to
  created or open, and its season cannot return to created or open. The
  calendar's history is durable the moment the first whistle blows.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: trigger
write_scope: cross-row
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
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
    - started_match_blocks_status_regression
    - started_match_blocks_round_regression
    - started_match_blocks_season_regression
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/gates_test.go::TestMatchFreezeGate
    - internal/schedule/internal/repository/gates_test.go::TestRoundFreezeGate
    - internal/schedule/internal/repository/gates_test.go::TestSeasonFreezeGate
  e2e:
    - internal/e2e/matches_test.go::TestPostponeStartedMatchFrozen
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::A started match cannot be postponed
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Three triggers over one shared fact ("has a match of this row's subtree started?") — matches_freeze_gate keys on OLD.starts_at, the round and season gates call round_has_started_match / season_has_started_match, so the predicate needs a sibling read and uses OLD: only a trigger can say it. The gates fire on the status value, not on time passing, which is why an edit that moves starts_at forward cannot un-start a match. The refusal is P0001 with three distinct raise names but a single meaning; each repository maps it by SQLSTATE, not by name (persistence.IsTriggerViolation), to ErrRecordInUse, and the handlers answer 409 with the "hierarchy cannot move to an earlier stage" message. Known asymmetry: a return to in_progress is not in the gates' condition, so only R-SCHEDULE-003 refuses it. The split is explicit since 000019: these three functions own the started half (a started match may not become created/postponed; a round or season with a started match may not become created/open) and the same functions now also carry R-SCHEDULE-003's rank half, checked first and raising its own names (match_status_regression / round_status_regression / season_status_regression); both halves can refuse the same started row, so the order is the contract: a rank refusal never reports the started message. Two migration sites for the same functions: 000001 created them, 000019 holds their current bodies (000014's two-site pattern).
