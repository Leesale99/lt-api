---
id: R-GAME-014
title: One registration per principal per season
statement: |
  A person may be registered in a season only once. A second registration for
  the same principal in the same season is refused as a duplicate, so a
  player never holds two season entries and two token balances.
module: game
entity: player
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000004_add_permissions.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Insert
    layer: repository
    role: advisory-copy
    pinned_by: null
refusal:
  code: "23505"
  constraint:
    - players_season_id_user_id_key
  sentinel: ErrDuplicateRecord
  http: 409
fixture: null
verification:
  unit: []
  e2e:
    - internal/e2e/players_test.go::TestCreatePlayerDuplicateRegistration
  gherkin:
    - docs/spec/features/game/players.feature::A second registration in the same season is refused as a duplicate
    - docs/spec/features/game/players.feature::The database refuses a second registration for the same member
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The uniqueness is (season_id, user_id); the mapping in the adapter keys on the constraint name, so a rename turns the 409 into a 500 — that is why the name is spelled out here as well as in the migration. The column is nullable, so the constraint says "at most one entry per *known* user": any number of ownerless rows may coexist, which is what keeps the historical "no owner" shape representable. The column carries no foreign key to users — 000016 removed players_user_id_fkey — so a deleted user does not strip the reference. Verification gap: only the E2E duplicate probe pins this (no unit test, no direct-SQL constraint probe like TestRideMoneyStateCoupling — a candidate for the same treatment). Refusal code note: 23505 (unique violation) is not in the schema's house table (P0001 / 23514 / 23503 / 23001 / synthetic); it is recorded truthfully here because `synthetic` is reserved for a refusal with no SQLSTATE at all.
