---
id: R-GAME-026
title: The calendar is reached through game's own vocabulary, never its tables
statement: |
  game learns calendar facts only through the calendar's game-facing
  operations and its events, and every refusal arrives as one of game's own
  errors; a phase or a value game does not recognize is a loud error, not a
  guess. The one thing game may not do — read or lock the calendar's rows
  itself — is enforced by the database, with the two write-instant round
  guards as the only exceptions and the calendar's own functions as their
  implementation.
module: game
entity: ride
category: boundary
status: implemented
authority: composition-root
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/game/internal/adapter/outbound/schedule_acl.go::ScheduleACL
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/schedule_events.go::Subscribe
    layer: repository
    role: authoritative
    pinned_by: null
  - site: migrations/000018_context_roles.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrNotFound
  http: 404
fixture: null
verification:
  unit:
    - internal/game/internal/core/service/service_test.go::TestService_RideCreateUnknownParents
    - internal/game/internal/core/service/service_test.go::TestService_RideLock
    - internal/game/internal/core/service/service_test.go::TestService_RideCreate
  e2e: null
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority is the composition root: which facade each context receives, and the direction of the dependency (game → schedule, game → ledger, never the reverse), is decided at wiring time; the anti-corruption layer is the only game package allowed to import the calendar, and its translation table is the contract that keeps the direction. The layer value is `repository` because it is the closest the frozen enum has to "adapter"; the decision itself is composition. Refusal mapping this rule owns (the per-command consequences are other rules'): the calendar's not-found becomes game's not-found (404, which the client sees as an unknown team/match/season); a closed round's "no phase exists" becomes the round refusal before any phase is derived; a team with no next match becomes the no-next-match conflict, distinct from a missing ride. Two loud-error branches are part of the statement: an unknown phase name fails instead of defaulting, and a ride's team that sits on neither side of a match refuses rather than guessing odds. The privilege half (000018) is what makes the boundary real rather than a convention: game's role holds CRUD on its own schema only, everything in the calendar's schema is denied, and the two write-instant guards are SECURITY DEFINER functions the game role may only execute — schedule's SQL, schedule's owner, the caller's transaction. The committed snapshot also carries the odds and the payload's rate claim is verified against it (R-GAME-010), so nothing the settlement consumes arrives unverified any more. Renumbered at merge (was provisional R-GAME-028): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely. Related: R-PLATFORM-013 is the general rule (contexts depend one way and meet only at facades); this one is the publisher-language/ACL half at game's edge — a distinct, independently falsifiable fact, so it stays its own rule.
