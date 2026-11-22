---
id: R-SCHEDULE-009
title: Odds are a decimal above one, recorded to two places
statement: |
  A match's home and away odds are both strictly greater than one — a book
  never offers a stake back for nothing — and are recorded to at most two
  decimal places, the precision the bonus arithmetic is derived from.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: migrations/000007_alter_matches_odds_precision.up.sql
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
    - matches_home_odds_check
    - matches_away_odds_check
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch
  e2e: null
  gherkin:
    - docs/spec/features/schedule/fixtures.feature::The database refuses a fixture priced at evens or below
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  one finding against 000007's prose. (1) The migration says a 3-dp value is "unrepresentable, not rounded on arrival" — that is false: a numeric typmod coerces by *rounding*, so an odds of 1.234 arrives as 1.23 silently, and nothing (no CHECK on round(odds,2)) refuses it. The rule as the database actually enforces it is "at most two places are *stored*", not "a third place is refused"; a CHECK would be needed to make the stated claim true. The create path therefore echoes the stored odds, not the requested ones: MatchRepository.Insert's RETURNING carries home_odds/away_odds and the 201 reports the row's rounded value (1.234 in, 1.23 out — the pattern 000020 set for ended_at on the resolve path), so the discrepancy between request and row is visible in the response instead of hidden behind an in-memory copy. Third, unremarked: precision 4 caps odds at 99.99, and a larger value surfaces as 22003 (numeric overflow), which is unmapped -> 500.
tie: null
---

## Notes

Two halves, one operand contract (ADR-021's scale chain: bonus_acc scale 4 = tokens 0 + odds 2 + streak-rate budget 2). The bound half is a real refusal (23514, app-mirrored, unmapped in the repository so a bypassing writer sees a 500); 000007 narrowed numeric(6,3) to numeric(4,2).
