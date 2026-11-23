---
id: R-SCHEDULE-003
title: A schedule lifecycle never moves backwards
statement: |
  A season, a round and a match only ever advance through their vocabularies:
  created, then open (or in progress), then closed. A status may not return to
  an earlier stage, a same-status rewrite is allowed, and a closed match is
  final — it never changes state again, even back to in_progress.
module: schedule
entity: season
category: policy
status: implemented
authority: database
mechanism: trigger
write_scope: row-local
enforcement:
  - site: migrations/000019_add_status_rank_gates.up.sql
    layer: db-trigger
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/model/status.go::checkStatusRegression
    layer: domain
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/model/season.go::ValidateSeasonUpdate
    layer: validator
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/model/round.go::ValidateRoundUpdate
    layer: validator
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/model/match.go::ValidateMatchUpdate
    layer: validator
    role: advisory-copy
    pinned_by: null
refusal:
  code: P0001
  constraint:
    - match_status_regression
    - round_status_regression
    - season_status_regression
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/model/lifecycle_test.go::TestValidateSeasonUpdate
    - internal/schedule/internal/model/lifecycle_test.go::TestValidateRoundUpdate
    - internal/schedule/internal/model/matches_validation_test.go::TestValidateMatchUpdate
    - internal/schedule/internal/repository/constraints_test.go::TestStatusRankGatesMirrorGoRanks
    - internal/schedule/internal/repository/gates_test.go::TestRoundFreezeGate
    - internal/schedule/internal/repository/gates_test.go::TestSeasonFreezeGate
  e2e:
    - internal/e2e/rounds_test.go::TestUpdateRoundHandler
    - internal/e2e/matches_test.go::TestUpdateMatchHandler
  gherkin:
    - docs/spec/features/schedule/lifecycle.feature::A closed match cannot go back to running
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md
stale_pointer: |-
  migration 000008's header ("Regressions ... are NOT gated here — they belong to rounds_freeze_gate, which already refuses them") overstates the gate — resolved by 000019: it refused regressions only once a match of the round had started, and the rank half now lives in the same function's 000019 body, so the 000008 header is historical, not current; 000008 is applied and stays byte-identical.
tie: null
---

## Notes

One rule, three vocabularies: each entity owns a rank map and the shared statusRegresses verdict rejects a lower rank (disjoint cases, one statement; the generic signature makes a cross-vocabulary comparison a compile error). The rank maps are why a same-status rewrite passes. Since migration 000019 the database is the authoritative home: the three gate functions mirror the Go ranks and refuse every regression with the three P0001 names (match_status_regression / round_status_regression / season_status_regression), including a closed match moved back to in_progress, a closed round with nothing started moved back to open, and a live season moved back a stage — all reachable by a direct writer before. The rank check runs BEFORE the started-freeze rule, so a row tripping both reports the rank message deterministically. The Go rank maps stay the authority the DB copies must not drift from: the model's three exported StatusRegresses functions are the oracle and the drift matrix (TestStatusRankGatesMirrorGoRanks) probes every ordered status pair, asserting the exact rank message on regressions and acceptance elsewhere (fixtures isolate the rank rule from the freeze / season-live / sequence gates). The advisory validators still run first, so the API path keeps its friendly 422 and the DB's 409 is reachable only by writers that skip them.
