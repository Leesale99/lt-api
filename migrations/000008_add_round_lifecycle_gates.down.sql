-- The gate triggers must go first: DROP FUNCTION defaults to RESTRICT, so
-- with the triggers still attached the drop fails (000006 avoided this only
-- because dropping the rides table cascaded its trigger; rounds survives
-- this down path).
DROP TRIGGER IF EXISTS rounds_close_gate ON rounds;
DROP TRIGGER IF EXISTS rounds_progress_gate ON rounds;
DROP FUNCTION IF EXISTS rounds_block_illegal_close();
DROP FUNCTION IF EXISTS rounds_block_illegal_progress();
