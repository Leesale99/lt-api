-- Reverse of 000015: every table and gate function back to public, the
-- three context schemas dropped. Order matters only for readability: the
-- table moves and the function moves are independent (triggers reference
-- functions by OID, not by schema-qualified name), so either order works.

-- The function-level search_path pins go first (while the qualified
-- names still resolve), before the functions move back to public.
ALTER FUNCTION schedule.round_has_started_match(bigint) RESET search_path;
ALTER FUNCTION schedule.season_has_started_match(bigint) RESET search_path;
ALTER FUNCTION schedule.matches_block_started_regression() RESET search_path;
ALTER FUNCTION schedule.rounds_block_started_regression() RESET search_path;
ALTER FUNCTION schedule.seasons_block_started_regression() RESET search_path;
ALTER FUNCTION schedule.seasons_block_delete() RESET search_path;
ALTER FUNCTION schedule.rounds_block_delete() RESET search_path;
ALTER FUNCTION schedule.matches_set_ended_at_on_close() RESET search_path;
ALTER FUNCTION schedule.rounds_block_illegal_progress() RESET search_path;
ALTER FUNCTION schedule.rounds_block_illegal_close() RESET search_path;
ALTER FUNCTION game.rides_block_illegal_transition() RESET search_path;
ALTER FUNCTION game.rides_block_illegal_insert() RESET search_path;

ALTER FUNCTION schedule.round_has_started_match(bigint) SET SCHEMA public;
ALTER FUNCTION schedule.season_has_started_match(bigint) SET SCHEMA public;
ALTER FUNCTION schedule.matches_block_started_regression() SET SCHEMA public;
ALTER FUNCTION schedule.rounds_block_started_regression() SET SCHEMA public;
ALTER FUNCTION schedule.seasons_block_started_regression() SET SCHEMA public;
ALTER FUNCTION schedule.seasons_block_delete() SET SCHEMA public;
ALTER FUNCTION schedule.rounds_block_delete() SET SCHEMA public;
ALTER FUNCTION schedule.matches_set_ended_at_on_close() SET SCHEMA public;
ALTER FUNCTION schedule.rounds_block_illegal_progress() SET SCHEMA public;
ALTER FUNCTION schedule.rounds_block_illegal_close() SET SCHEMA public;
ALTER FUNCTION game.rides_block_illegal_transition() SET SCHEMA public;
ALTER FUNCTION game.rides_block_illegal_insert() SET SCHEMA public;

ALTER TABLE schedule.teams SET SCHEMA public;
ALTER TABLE schedule.seasons SET SCHEMA public;
ALTER TABLE schedule.rounds SET SCHEMA public;
ALTER TABLE schedule.matches SET SCHEMA public;

ALTER TABLE game.players SET SCHEMA public;
ALTER TABLE game.rides SET SCHEMA public;
ALTER TABLE game.idempotency_keys SET SCHEMA public;

ALTER TABLE identity.users SET SCHEMA public;
ALTER TABLE identity.user_tokens SET SCHEMA public;
ALTER TABLE identity.roles SET SCHEMA public;
ALTER TABLE identity.permissions SET SCHEMA public;
ALTER TABLE identity.roles_permissions SET SCHEMA public;

DROP SCHEMA IF EXISTS schedule;
DROP SCHEMA IF EXISTS game;
DROP SCHEMA IF EXISTS identity;
