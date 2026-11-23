---
id: R-GAME-033
title: A burn is announced as a ride.burned event
statement: |
  A burned ride is announced as a ride.burned event carrying the ride's identity, so
  downstream consumers react to the committed burn instead of being called from inside
  the burn.
module: game
entity: ride
category: orchestration
status: specified-only
authority: service
mechanism: null
write_scope: null
enforcement: []
refusal: null
fixture: null
verification:
  unit: null
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Specified only. Phase 08 §"First useful events" lists ride.burned in the tiny vocabulary, §"Example transaction" appends the event in the burn's own transaction (payload {"ride_id": ...}), and §"Ranking consumer example" consumes it (HandleRideBurned). Nothing publishes it: game is a consumer only (internal/game/internal/adapter/outbound/schedule_events.go), schedule publishes MatchResultRecorded and RoundCompleted, and no ride.* or player.* event type exists. The same-transaction append guarantee is catalogued separately as R-KERNEL-015; this fragment is the vocabulary half. Consumers do not exist either — rankings has no code (phase-05 says so) and the module enum has no rankings value.
