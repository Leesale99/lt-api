-- The rides_state_gate trigger vanishes with its table below, but the
-- function is standalone and would survive the table drop — without this
-- drop a down/up cycle fails on "function already exists".
DROP FUNCTION IF EXISTS rides_block_illegal_transition();

DROP TABLE IF EXISTS rides;
