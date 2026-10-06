-- INSERT half of the ride state gate (ADR-019/ADR-020): rides are created
-- locked, and that creation contract is now DB-enforced too. The UPDATE
-- gate (000006, extended by 000011) only saw UPDATEs, so a direct INSERT —
-- script, future writer, a test shortcut — could plant a ride in any
-- state, outside every result-agreement and transition rule. This closes
-- that hole: a ride may only be born locked. Every other state must be
-- reached through a transition the UPDATE gate checks.
--
-- P0001, the house convention: the store maps the refusal to
-- ErrInvalidTransition (409), like every other gate refusal.

CREATE FUNCTION rides_block_illegal_insert() RETURNS trigger AS $$
BEGIN
  IF NEW.state IS DISTINCT FROM 'locked' THEN
    RAISE EXCEPTION 'ride_insert_requires_locked_state'
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER rides_state_insert_gate
  BEFORE INSERT ON rides
  FOR EACH ROW EXECUTE FUNCTION rides_block_illegal_insert();
