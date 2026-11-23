---
id: R-GAME-018
title: A ride command claims its key before it runs
statement: |
  Every ride command first claims its idempotency key inside its own
  transaction, and only then reads, gates and writes; a command whose key is
  already completed returns the recorded answer without executing. The claim
  commits with the answer, so a failed command leaves nothing recorded and its
  retry runs for real.
module: game
entity: idempotency_key
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideCreate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideLock
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideBurn
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/core/service/ride_commands.go::Service.RideUnlock
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideIdempotentHandler
    - internal/e2e/rides_test.go::TestRideLockIdempotentHandler
  gherkin:
    - docs/spec/features/game/idempotency.feature::A retry under the same key comes back with the recorded answer
expert-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Four authoritative sites, one per command: the ordering (claim first, then execute, then store, then commit) is the same statement applied to four disjoint commands, so it is one rule with four sites, not four rules. No refusal: a claim that loses is not a refusal — it returns the recorded answer, which is a success. The recorded answer is the transport's presentation callback output (status + marshaled body), stored byte for byte so the replay is byte-identical, and create's Location header is re-derived from the stored body rather than persisted. The claim table and the single-flight mechanics are the kernel engine's; what this rule owns is that the *game* commands call it first and inside one transaction. The caller is part of the claim's identity since the ride routes became permission-gated: the handlers put the authenticated principal's id on the token (`port.Token.UserID`) and the kernel claims (key, user_id), so two callers that pick the same key hold two claims (R-KERNEL-003 owns the scope and the retention window). Related but distinct: R-KERNEL-004 owns the transactional coupling (the claim commits with the command's effect, or not at all); this rule owns the ordering inside the command, and the kernel claim rule (R-KERNEL-003) owns what the caller sees on a replay.
