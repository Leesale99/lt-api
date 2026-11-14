-- Schema-per-context: the context boundaries become
-- enforceable BY POSTGRES ITSELF. Three schemas, one per bounded context
-- (ADR-028's ownership map, made physical):
--
--   schedule  teams, seasons, rounds, matches (+ their internal gates)
--   game      rides, players, idempotency_keys (game-side event
--             idempotency lives here; the outbox gets its own home in
--             Phase 08)
--   identity  users, user_tokens, roles, permissions, roles_permissions
--
-- Everything moves in one migration so no intermediate layout is
-- reachable: before it, every table is public (one flat namespace,
-- unqualified store SQL); after it, the role work of 000018 can grant
-- per-context privileges on real schemas.
--
-- Mechanics:
--   - ALTER TABLE SET SCHEMA moves the table WITH its constraints,
--     indexes, sequences (IDENTITY) and triggers; the trigger functions
--     themselves are standalone objects and move via ALTER FUNCTION.
--   - The functions move into their table's schema so the gate SQL inside
--     them resolves under each context role's search_path (000018 sets
--     search_path to the role's own schema; trigger execution runs with
--     the invoking role's privileges, so the schedules gates read
--     schedule tables as schedule_app — inside the schema, no grant
--     needed beyond the table privileges the role already holds).
--   - No REINDEX needed: index definitions are attached to their table
--     and move with it; the trgm opclass lives on the pg_trgm extension
--     in public, which the search_path's implicit public entry resolves.
--   - Cross-context FOREIGN KEYs are NOT touched here — they are the
--     next migration (000016), so each surgery step is independently
--     revertable.

CREATE SCHEMA IF NOT EXISTS schedule;
CREATE SCHEMA IF NOT EXISTS game;
CREATE SCHEMA IF NOT EXISTS identity;

-- schedule: the calendar context.
ALTER TABLE teams SET SCHEMA schedule;
ALTER TABLE seasons SET SCHEMA schedule;
ALTER TABLE rounds SET SCHEMA schedule;
ALTER TABLE matches SET SCHEMA schedule;

-- game: the ride gameplay context.
ALTER TABLE players SET SCHEMA game;
ALTER TABLE rides SET SCHEMA game;
ALTER TABLE idempotency_keys SET SCHEMA game;

-- identity: who logs in and what they may do.
ALTER TABLE users SET SCHEMA identity;
ALTER TABLE user_tokens SET SCHEMA identity;
ALTER TABLE roles SET SCHEMA identity;
ALTER TABLE permissions SET SCHEMA identity;
ALTER TABLE roles_permissions SET SCHEMA identity;

-- The ADR-008 freeze gates + the round lifecycle gates (000008) and the
-- ended_at stamp (000005): all schedule-internal (they read only
-- seasons/rounds/matches), so they move with their tables.
ALTER FUNCTION round_has_started_match(bigint) SET SCHEMA schedule;
ALTER FUNCTION season_has_started_match(bigint) SET SCHEMA schedule;
ALTER FUNCTION matches_block_started_regression() SET SCHEMA schedule;
ALTER FUNCTION rounds_block_started_regression() SET SCHEMA schedule;
ALTER FUNCTION seasons_block_started_regression() SET SCHEMA schedule;
ALTER FUNCTION seasons_block_delete() SET SCHEMA schedule;
ALTER FUNCTION rounds_block_delete() SET SCHEMA schedule;
ALTER FUNCTION matches_set_ended_at_on_close() SET SCHEMA schedule;
ALTER FUNCTION rounds_block_illegal_progress() SET SCHEMA schedule;
ALTER FUNCTION rounds_block_illegal_close() SET SCHEMA schedule;

-- The ride state machine gates (000006/000012): row-local against rides
-- (NEW/OLD only), so they stay game-internal. The one cross-context half
-- — result agreement reading matches (000011, check 5) — leaves the
-- trigger body in 000017 and becomes game Go code at the settlement
-- paths; the body below still carries check 5 until that migration.
ALTER FUNCTION rides_block_illegal_transition() SET SCHEMA game;
ALTER FUNCTION rides_block_illegal_insert() SET SCHEMA game;

-- Every gate body reads its tables UNQUALIFIED (history migration text —
-- left untouched). Before the split that was harmless: one flat schema.
-- After it, a body resolved through the CALLER's search_path breaks for
-- any caller whose path lacks the table's schema — the owner role (the
-- seed and the test fixtures write through it) has only "$user", public.
-- Pin each gate's search_path to its own schema at the FUNCTION level:
-- the body then resolves inside its context regardless of who fires the
-- trigger. (pg_catalog stays implicitly first.)
ALTER FUNCTION schedule.round_has_started_match(bigint) SET search_path = schedule;
ALTER FUNCTION schedule.season_has_started_match(bigint) SET search_path = schedule;
ALTER FUNCTION schedule.matches_block_started_regression() SET search_path = schedule;
ALTER FUNCTION schedule.rounds_block_started_regression() SET search_path = schedule;
ALTER FUNCTION schedule.seasons_block_started_regression() SET search_path = schedule;
ALTER FUNCTION schedule.seasons_block_delete() SET search_path = schedule;
ALTER FUNCTION schedule.rounds_block_delete() SET search_path = schedule;
ALTER FUNCTION schedule.matches_set_ended_at_on_close() SET search_path = schedule;
ALTER FUNCTION schedule.rounds_block_illegal_progress() SET search_path = schedule;
ALTER FUNCTION schedule.rounds_block_illegal_close() SET search_path = schedule;
ALTER FUNCTION game.rides_block_illegal_transition() SET search_path = game;
ALTER FUNCTION game.rides_block_illegal_insert() SET search_path = game;
