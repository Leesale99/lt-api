-- Standalone SQL objects created by the up migration. The five triggers
-- vanish with their tables below, but the functions are standalone and
-- would survive the table drops — without these drops a down/up cycle
-- fails on "function already exists".
DROP TRIGGER IF EXISTS matches_freeze_gate ON matches;
DROP TRIGGER IF EXISTS rounds_freeze_gate ON rounds;
DROP TRIGGER IF EXISTS seasons_freeze_gate ON seasons;
DROP TRIGGER IF EXISTS seasons_delete_gate ON seasons;
DROP TRIGGER IF EXISTS rounds_delete_gate ON rounds;

DROP FUNCTION IF EXISTS matches_block_started_regression();
DROP FUNCTION IF EXISTS rounds_block_started_regression();
DROP FUNCTION IF EXISTS seasons_block_started_regression();
DROP FUNCTION IF EXISTS seasons_block_delete();
DROP FUNCTION IF EXISTS rounds_block_delete();
DROP FUNCTION IF EXISTS round_has_started_match(bigint);
DROP FUNCTION IF EXISTS season_has_started_match(bigint);

DROP TABLE IF EXISTS matches;
DROP TABLE IF EXISTS rounds;
DROP TABLE IF EXISTS players;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS seasons;
