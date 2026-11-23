---
id: R-GAME-027
title: The round-close sweep is served by the undecided-rides partial index
statement: |
  The query that collects a closing round's undecided rides is the one shape
  the database is tuned for: it filters rides by match and by the single
  undecided state, and a partial index covers exactly that subset. The index
  stays small because rides leave that state at their round's close, so the
  round-close cost does not grow with the lifetime ride history.
module: game
entity: ride
category: persistence-fact
status: implemented
authority: repository
mechanism: null
write_scope: row-local
enforcement:
  - site: migrations/000009_add_rides_won_pending_partial_index.up.sql
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.UndecidedForMatchesTx
    layer: repository
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-01-postgresql-persistence.md
stale_pointer: |-
  migration 000009's header says the predicate serves "the rounds_close_gate EXISTS backstop and the auto-unlock/auto-burn UPDATE ... FROM". Both moved: 000014 dropped rounds_close_gate's unresolved-rides check (undecided rides in a closing round are now the designed intermediate state) and the auto-unlock statement left the close transaction for game's settlement handler in internal/game (000014's header says so explicitly). The index still holds and still serves the query it serves; only the prose naming its consumers is stale. The applied migration is immutable, so the fix is this note plus a comment at the current query site, not a migration rewrite. Renumbered at merge (was provisional R-GAME-029): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely.
tie: null
---

## Notes

A persistence fact, not a business rule: nothing is refused if the index disappears — the sweep just gets slower — which is why the refusal is null and the verification is a functional pointer at the sweep rather than a performance assertion. The stated guarantee is a matching obligation in one direction: the index (`match_id`, where the state is the undecided one) serves the sweep only while the sweep keeps that predicate — a query that started filtering, say, on a joined round id would silently lose the index. The other consumer the index was measured for is gone.
