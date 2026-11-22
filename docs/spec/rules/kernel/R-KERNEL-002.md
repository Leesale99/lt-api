---
id: R-KERNEL-002
title: A request's dedup identity is a digest of the request itself
statement: |
  Two requests are the same logical request exactly when they carry the same
  method, the same request URI and — for a command with a body — the same raw body
  bytes. The identity is a SHA-256 digest of exactly those bytes, and a body is
  capped before it is hashed so that the bytes hashed are the bytes decoded: a
  retry of an identical request is recognized as a replay, any changed field makes
  it a different request, and an oversized body claims no key at all.
module: kernel
entity: idempotency_key
category: orchestration
status: implemented
authority: handler
mechanism: null
write_scope: row-local
enforcement:
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.rideCommandToken
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken
    layer: handler
    role: authoritative
    pinned_by: null
  - site: migrations/000010_add_idempotency_keys_table.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent
  e2e:
    - internal/e2e/rides_test.go::TestCreateRideIdempotentHandler
    - internal/e2e/rides_test.go::TestRideLockIdempotentHandler
    - internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the body drain is what makes the digest and the decoded body the same bytes; capping the body only at decode would hash more bytes than a retry re-reads and turn an identical retry into a 409, and the e2e replay subtest fails first.
---

## Notes

Merged rule (Phase B3): the kernel sweep stated the hash contract and the game sweep catalogued the same statement as the ride edge's request identity (a game fragment whose draft slot was later reused and has since been retired), so they are one rule with the union of sites — module kernel, because the kernel owns the claim/hash contract and game only instantiates it at /v1/rides (the IRequestToken/claim seam the game handler feeds). One request shape, one formula: every ride command — create and the body-less lock/burn/unlock alike — hashes method + "\n" + URI + "\n" + the raw body bytes, which is why both token builders drain and restore r.Body. The body-less commands used to hash method + URI only, so appending a body to POST /v1/rides/:id/burn left the dedup identity unchanged (misalignment-report §6 item 15, recorded then fixed): the shared formula makes the statement true for every command instead of only the one that decodes a body, and the e2e "same key with different body bytes" case pins it. Deployment note: the formula change is not backward compatible with keys claimed under the old one — a row stored before the deploy carries a URI-only digest, so a retry of that request afterwards is the 409 idempotency conflict, not a replay; the table sweeps rows older than a day (R-KERNEL-003, migration 000022), so those stale rows disappear within that window instead of living forever. The table's request_hash CHECK (exactly 32 bytes) is the only backstop and it cannot verify the digest covers the right bytes; a second ride-shaped command must remember createRideToken's shape by hand. The cap-before-hash half is not decoration: the size limit wraps the body before it is drained, so an oversized body is refused with 413 before any hash exists and the key is discarded, which is what lets a correct retry with a legal body execute fresh instead of conflicting (R-KERNEL-013 owns the cap itself). No refusal of its own: a digest is a computation and refuses nothing — the 409 a changed request under one key produces is R-KERNEL-003's (which owns the caller-facing replay statement too), and the 413 is R-KERNEL-013's. Verification gap: the create-side and command-side e2e cases (same key, different body -> 409) are the pins on the body half; the formula's "same key, different ride" case is asserted at the service level with hand-built tokens, so nothing ties the URI half to a test — a change that dropped the URI from the hash would replay silently instead of conflicting. Category and authority note: `orchestration` with authority `handler`, deviating from the category default (service) — the dedup identity is framed from the raw request bytes (method, URI, body) at the transport edge, and the use case only ever receives the finished token. It refuses nothing itself, so it cannot name the refusal status the `interface` category requires (policy 14); the 409 and 413 it makes possible belong to R-KERNEL-003 and R-KERNEL-013.
