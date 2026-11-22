---
id: R-SCHEDULE-021
title: A match is resolved once, against the version the caller holds
statement: |
  A result may only be written for a match that is in progress, has already
  started, has no end yet, whose round is still open, and whose version is the
  one the caller was shown. A second resolution, a stale caller or a closed
  round is refused and changes nothing.
module: schedule
entity: match
category: orchestration
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/schedule/internal/repository/match_repo.go::MatchRepository.ResolveTx
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/repository/match_repo.go::MatchRepository.classifyResolveRefusal
    layer: repository
    role: advisory-copy
    pinned_by: null
  - site: internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch
    layer: service
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrEditConflict
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/rides_concurrency_test.go::TestResolveMatchRaces
  e2e:
    - internal/e2e/matches_test.go::TestResolveMatchHandler
    - internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce
  gherkin:
    - docs/spec/features/schedule/resolve.feature::A result written against an outdated reading is refused
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: |-
  ResolveMatch's comment still says the payload's winner "is re-checked ... through the rides_state_gate", but 000017 removed that cross-context check from the trigger; the re-check is now game's resultAgreement in Go (R-GAME-010). The guarded UPDATE's RETURNING now carries ended_at, so ResolveTx returns the committed row rather than a pre-seeded request copy; the whole 200 body (and the post-commit MatchResultRecorded) is the row's values, which makes future trigger drift visible at the wire.
tie: |-
  the service site fails loudly — a wrong boundary loses the serialization the race test asserts, so the refused write surfaces there first.
---

## Notes

Authority deviates from the orchestration default (service) because the predicate needs the write instant: it is spelled as the UPDATE's own WHERE (status = 'in_progress' AND starts_at < now() AND ended_at IS NULL AND version = $n AND EXISTS(round still open)), so a blocked write re-evaluates on lock release and 0 rows is the refusal — path-dependent by definition, with no call-set fixture, so a new resolve statement that forgot the guard would pass every test. The service site is the transaction boundary and the ordering (time policy first, then the guarded write) and is listed as defense-in-depth. Zero rows is ambiguous by construction (unknown match, stale version, wrong status, closed round), so classifyResolveRefusal reads beside the transaction and names the cause: absent row -> ErrRecordNotFound (404), everything else -> ErrEditConflict (409). The classification is advisory in the strict sense — the refusal already happened, the read only chooses the status code.
