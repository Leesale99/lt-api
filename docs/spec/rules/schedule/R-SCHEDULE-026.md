---
id: R-SCHEDULE-026
title: A calendar record the game context still references is not deleted
statement: |
  The calendar may not delete a team, a match or a season that the game context
  still points at: before deleting it asks game through schedule's own port, and
  refuses with the in-use conflict when any ride, player favourite or registration
  still references the record. For a season the question is asked of its
  registrations too, so a registration that still carries rides keeps the whole
  delete refused.
module: schedule
entity: team
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: cross-table
enforcement:
  - site: internal/schedule/internal/service/delete_policy.go::Service.DeleteTeam
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/service/delete_policy.go::Service.DeleteMatch
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/schedule/internal/service/delete_policy.go::Service.DeleteSeason
    layer: service
    role: authoritative
    pinned_by: null
  - site: cmd/api/gamerefs.go::gameRefs
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ExistsForMatch
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ExistsForTeam
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.DeleteForSeasonTx
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: migrations/000016_drop_cross_context_foreign_keys.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
  - site: migrations/000021_add_game_delete_players_for_season.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrRecordInUse
  http: 409
fixture: null
verification:
  unit:
    - internal/schedule/internal/service/rounds_close_test.go::TestMatchDeleteRestricted
  e2e:
    - internal/e2e/matches_test.go::TestDeleteMatchHandler
    - internal/e2e/teams_test.go::TestDeleteTeamHandler
    - internal/e2e/seasons_test.go::TestDeleteSeasonHandler
    - internal/e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers
  gherkin:
    - docs/spec/features/schedule/deletion.feature::A match a ride still sits on cannot be deleted
expert-ref: null
knowledge-ref: null
stale_pointer: |-
  000016's header still describes the replacement as "the api layer (api holds the identity store)" and "the api season-delete handler" / "PlayerStore"; internal/api no longer exists — the delete policies are internal/schedule/internal/service/delete_policy.go, the adapter is cmd/api/gamerefs.go and the store is game's own repository.
tie: |-
  they cannot drift into a wrong answer without failing the calendar's own delete test), and the composed check cannot silently disappear because the port is required at wiring time.
---

## Notes

Merged rule (Phase B3): the calendar sweep (provisional R-SCHEDULE-026) and the game sweep (provisional R-GAME-027) found one boundary fact from its two ends, so it is one rule with the union of sites — module schedule, because the side that performs the delete owns the delete policy, and game's slots are freed for dense renumbering. The decision is per record (team: player favourites and rides; match: rides; season: its registrations and their rides) and the order is the record's own: team and match ask game, then delete; a season deletes its registrations through game first and the season row second, both inside schedule's one transaction (000021's definer function + SeasonRepository.DeleteTx), which is why the authority is the use case. The question is asked through GameUsage, a consumer-side port declared in schedule's service package so that game -> schedule stays the only legal import direction; the composition root adapts game's facade to it (cmd/api/gamerefs.go::gameRefs) and translates game's "still referenced" sentinel into schedule's ErrRecordInUse, so the handlers only ever see one vocabulary — that translation is the fourth authoritative site. Category orchestration rather than the game sweep's boundary: the port's answers are facts and the refusal is still the use case's, so the merged rule keeps one authority layer instead of splitting into a wiring rule plus a policy rule (no composition-root authority is claimed here; the wiring itself is R-PLATFORM-013's). Refusal surface: both refusals are Go-only (no SQLSTATE — the cross-context FKs that used to produce them were dropped by 000016) and the same 409 the FKs used to give; the one surviving same-context FK, rides_player_id_fkey RESTRICT, raises 23503 on the season path and is translated there. Two recorded gaps: (1) the answer is a check-then-delete, so a ride locked or a player created between the read and the write slips through — accepted, and exactly the window the old delete-then-FK flow had; (2) an unwired port is a 500 (errGameRefsNotWired), not a refusal. The game-side EXISTS queries and DeleteForSeasonTx are facts, not decisions (TestMatchDeleteRestricted is the deliberate opposite assertion — it proves the store no longer refuses, pinning the boundary that this policy is the only thing holding the rule. The season half is pinned end to end since Phase G4: TestDeleteSeasonRefusalKeepsSeasonPlayers plants a ride on one registration (this rule's game-side refusal) and a started match (R-SCHEDULE-023's gate), and asserts the 409 and that the registrations survive — the same one transaction rolls both refusals back, so a refused season delete cannot leave the season alive with its players already gone. The row-outcome half of the season path (the players really go with the season, and a refused delete rolls them back) stays R-SCHEDULE-027; the durability gates stay R-SCHEDULE-023 and the same-context team half R-SCHEDULE-025.
