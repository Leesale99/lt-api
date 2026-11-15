-- The game half of the calendar's season delete, as a game (definer)
-- operation — the 000018 pattern with the direction reversed.
--
-- 000018 made the calendar's round guards the one piece of schedule SQL game
-- may run: game_app gets EXECUTE on schedule.round_open_guard /
-- schedule.destination_open_guard and no table privileges on schedule. This
-- migration is the mirror image of that grant: the SEASON delete runs on
-- schedule's transaction (schedule_app), and its game half — "delete every
-- player registered in the season" — is game's SQL. schedule_app has no
-- privilege on game.players, so the statement must be game's own SECURITY
-- DEFINER function, running as the schema owner inside the caller's
-- transaction. schedule_app gets EXECUTE and nothing else; even SELECT on
-- game.players stays denied to it.
--
-- Why the function and not a plain DELETE: the season delete has to be
-- all-or-nothing again. Migration 000016 dropped players.season_id ->
-- seasons' ON DELETE CASCADE (cross-context FK), so the calendar composes
-- the old cascade: players first, then the season row. Two statements on two
-- pools are two transactions — a season refused by its own delete gate
-- (000008's seasons_delete_gate, R-SCHEDULE-023) would lose its
-- registrations while it survived. One definer call on schedule's own
-- transaction puts the player delete back inside the same transaction as the
-- season delete, so the gate's P0001 and this statement's 23503 both roll
-- the whole composition back.
--
-- The body is the statement the game store already issued
-- (PlayerRepository.DeleteForSeason's `DELETE FROM players WHERE season_id =
-- $1`), unchanged: a ride still referencing a player trips
-- rides_player_id_fkey (RESTRICT, game-internal since 000016), 23503
-- propagates to the caller, and the caller maps it to its record-in-use
-- sentinel. No row-count contract: the DELETE is idempotent, a season with no
-- players is not a refusal.
--
-- SECURITY DEFINER + a pinned search_path (000018's shape): the body's
-- players name resolves inside the game schema regardless of the caller's
-- path, and the definer is the schema owner the migration runner created the
-- tables as. VOLATILE (the default, stated): the body writes rows, which only
-- volatile functions may do.
CREATE FUNCTION game.delete_players_for_season(season_id bigint) RETURNS void
LANGUAGE sql SECURITY DEFINER
SET search_path = game
VOLATILE
AS $$
  DELETE FROM players WHERE players.season_id = $1
$$;

-- EXECUTE for the calendar context and only it: PUBLIC's default EXECUTE is
-- revoked so no other role picks the surface up by accident (000018's grant
-- block, reversed direction — schedule_app calls it, game_app does not need
-- it: game deletes its own players through its own pool as it always did).
REVOKE ALL ON FUNCTION game.delete_players_for_season(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION game.delete_players_for_season(bigint) TO schedule_app;

-- USAGE on the game schema makes the function NAME callable — EXECUTE alone
-- is not enough (000018 granted the mirror of this: USAGE ON SCHEMA schedule
-- TO game_app). It opens no table: schedule_app still has no SELECT/INSERT/
-- UPDATE/DELETE on game.players, and the definer body runs as the schema
-- owner, so this one function is the only game surface the calendar owns.
GRANT USAGE ON SCHEMA game TO schedule_app;
