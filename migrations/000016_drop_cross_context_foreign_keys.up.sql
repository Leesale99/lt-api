-- Phase D: the cross-context FOREIGN KEYs come out. ADR-028's boundary
-- rule — other contexts consume a context's public operations, not its
-- tables — applied to the schema level: an FK is a coupling the referencing
-- context enforces inside the referenced context's storage, and after the
-- split it is exactly the shape of coupling the split exists to remove.
-- Each dropped constraint's check moves to the side that owns the writing:
--
--   players.user_id → users            game wrote it; identity owns users.
--                                      user existence is checked at the
--                                      api layer (api holds the identity
--                                      store) on player create — user_id
--                                      is only ever the authenticated
--                                      principal's ID, and PlayerStore.
--                                      Update never writes it.
--   players.season_id → seasons        game handlers validate the season
--                                      through app.Schedule before the
--                                      write (Phase C); the FK's CASCADE
--                                      on season delete moves to the api
--                                      season-delete handler, which now
--                                      deletes the season's players
--                                      through game first (same row
--                                      outcome, same 409 when a ride
--                                      still references a player).
--   players.favorite_team_id → teams   same handler-level existence check
--                                      (already there since Phase C); the
--                                      team-delete refusal for referenced
--                                      teams moves to the api delete
--                                      handler (409 preserved).
--   rides.match_id → matches           the ride-ref checks are the
--                                      SchedulePort's (RoundOpenTx /
--                                      DestinationOpenTx, Phase C); the
--                                      match-delete refusal for referenced
--                                      rides moves to the api delete
--                                      handler (409 preserved).
--   rides.team_id → teams              covered by the NextForTeam pick
--                                      (the team exists because a ride
--                                      command starts from its player's
--                                      data) and the api team-delete
--                                      check above.
--
-- What deliberately STAYS: every same-context FK (rounds→seasons,
-- matches→rounds/teams, rides→players, users→roles, user_tokens→users,
-- roles_permissions→roles/permissions) — those are internal to one
-- context's own consistency.
--
-- The referencing-side indexes (players_season_id_idx,
-- players_favorite_team_id_idx, rides_match_id_idx, rides_team_id_idx)
-- stay: they serve list filters and lookups, not the dropped FK
-- enforcement.

ALTER TABLE game.rides DROP CONSTRAINT rides_match_id_fkey;
ALTER TABLE game.rides DROP CONSTRAINT rides_team_id_fkey;

ALTER TABLE game.players DROP CONSTRAINT players_season_id_fkey;
ALTER TABLE game.players DROP CONSTRAINT players_favorite_team_id_fkey;
ALTER TABLE game.players DROP CONSTRAINT players_user_id_fkey;
