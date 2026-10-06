-- The trigger drops first: DROP FUNCTION defaults to RESTRICT, so with
-- rides_state_insert_gate still attached the drop would fail.
DROP TRIGGER IF EXISTS rides_state_insert_gate ON rides;
DROP FUNCTION IF EXISTS rides_block_illegal_insert();
