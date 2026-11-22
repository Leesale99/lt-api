---
id: R-KERNEL-003
title: An idempotency key names exactly one logical request
statement: |
  Once a key has been used by one caller, that same caller reusing it for a different
  request is refused as key misuse and the second request never executes. The refusal is
  a conflict, not a retry: a genuine retry resends the identical request and receives
  the stored answer. A key belongs to its caller — two callers that happen to choose the
  same key hold two independent claims, so one caller's key can neither collide with
  another's nor come back with another's answer. A claim is remembered for one day: a
  key whose claim is older than that is forgotten, and a command sent under it executes
  again instead of replaying.
module: kernel
entity: idempotency_key
category: policy
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/kernel/idempotency/idempotency.go::Repository.Claim
    layer: repository
    role: authoritative
    pinned_by: null
  - site: migrations/000022_scope_idempotency_keys_per_caller.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: migrations/000010_add_idempotency_keys_table.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/errors.go::Handler.idempotencyConflictResponse
    layer: handler
    role: advisory-copy
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrIdempotencyConflict
  http: 409
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent
  e2e:
    - internal/e2e/rides_test.go::TestRideLockIdempotentHandler
    - internal/e2e/rides_test.go::TestCreateRideIdempotentHandler
  gherkin:
    - docs/spec/features/game/idempotency.feature::The same key on a different command is refused, never replayed
    - docs/spec/features/game/idempotency.feature::Two callers that pick the same key do not collide
    - docs/spec/features/game/idempotency.feature::A claim older than the day it is kept is forgotten
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md
stale_pointer: null
tie: |-
  the claim's comparison is the only site that tells a genuine retry from key misuse (a misuse returns the sentinel, the handler renders 409, and the conflict subtest of both idempotency suites fails first).
---

## Notes

Authority deviates from the category default (policy → domain): the decision is not a business policy but a stored-fact comparison — "does this key already name a different request" — executed inside the claim, by the repository that owns the table. The table contributes the namespace and the lifetime instead: its primary key (key, user_id) since migration 000022 is what makes two callers' claims independent (misalignment-report §6 item 13, defect fixed: the key was the bare text in one global namespace, so two clients that picked the same key collided), and the claim's own sweep is what makes the day-long lifetime real. The handler site is an advisory copy in the refusal sense only: game translates the kernel's sentinel into the 409 envelope. Recorded gap: the kernel's own sentinel is unmapped outside game — any other context that adopts the key must add the mapping or answer 500. Scope, as built by 000022: the claim row is (key, user_id), the authenticated principal's id — the transport puts it on the token (`port.Token.UserID`, set by the ride handlers from httpx.GetAuthenticatedUser) and the settlement handlers claim as the reserved system caller 0, since an event has no request principal (0 is not an identity id: users.id is serial). The endpoint stays part of the request identity (the digest, R-KERNEL-002) rather than of the namespace, so one caller's key is one key across routes and a different route under it is still misuse. Legacy rows from 000010 were claimed before caller scoping existed; 000022 carries them over as system claims (user_id 0), which keeps the settlement events' own dedup intact across the deploy and leaves every ride retry that was in flight to execute once more. Retention, as built: the claim deletes rows older than 24 hours (`retentionWindow` in the kernel engine) on its own transaction before it inserts — a lazy sweep, one statement per claim, because a real background sweeper is the Phase 07 worker's job and R-PLATFORM-016 is still specified-only. The interim's boundary is recorded, not hidden: every claim pays for the sweep, the sweep is global (any caller's stale rows), and the window is a constant rather than config until the worker owns it. The Gherkin pair pins both halves — two callers on one key (both execute, neither is refused) and a claim aged past the window (the same key executes again). Merged at gate B5 (dupe policy): the game sweep had catalogued this same decision a second time, from the ride command's caller-facing side — same `Repository.Claim` site, same 409, same two idempotency suites — and it is now this rule's game instantiation, not an id of its own (retired R-GAME-019; see the header note above). What that statement contributes is the caller's half, and it belongs here because every future context inherits it: a genuine retry is never told "conflict" and never executes the command again; it receives the recorded answer byte for byte (status + raw bytes, never normalized JSON — migration 000010 chose bytea for exactly that reason); and single-flight is by insert conflict, so a concurrent duplicate blocks on the claim until the winner commits and then reads the committed answer (there is no in-flight state machine and no "in progress" status). The "missing row after a lost claim" path and a `Save` that matched no row are both the same conflict, not a silent re-execute. The game command call-sites that invoke this Claim are catalogued as the ordering on R-GAME-018 and as the transactional coupling on R-KERNEL-004 — they are enforcement sites of this one authority, not rules of their own.
