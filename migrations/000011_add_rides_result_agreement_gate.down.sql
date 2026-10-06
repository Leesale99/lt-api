-- Restore the 000006 body verbatim: the gate goes back to the pre-result-
-- agreement behavior exactly (checks 1–4, no result agreement). The trigger
-- stays attached to the replaced function.
CREATE OR REPLACE FUNCTION rides_block_illegal_transition() RETURNS trigger AS $$
BEGIN
  -- 1. Terminal rides are frozen: no state change, no match re-point.
  -- A state-preserving full-row rewrite (the store's Update shape) is not
  -- a transition and passes.
  IF OLD.state IN ('burned', 'lost', 'unlocked')
     AND (NEW.state IS DISTINCT FROM OLD.state
       OR NEW.match_id IS DISTINCT FROM OLD.match_id) THEN
    RAISE EXCEPTION 'ride_terminal_state_frozen'
      USING ERRCODE = 'P0001';
  END IF;

  -- 2. match_id re-points only on continuation (won_pending → locked,
  -- ADR-018: one FK write, nothing else may write it).
  IF NEW.match_id IS DISTINCT FROM OLD.match_id
     AND NOT (OLD.state = 'won_pending' AND NEW.state = 'locked') THEN
    RAISE EXCEPTION 'ride_match_id_immutable_outside_continuation'
      USING ERRCODE = 'P0001';
  END IF;

  -- 3. From-state graph: locked may only move to won_pending|lost;
  -- won_pending only to locked|burned|unlocked.
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT (
       (OLD.state = 'locked' AND NEW.state IN ('won_pending', 'lost')) OR
       (OLD.state = 'won_pending' AND NEW.state IN ('locked', 'burned', 'unlocked'))
     ) THEN
    RAISE EXCEPTION 'ride_state_transition_illegal'
      USING ERRCODE = 'P0001';
  END IF;

  -- 4. Money-state coupling: unlocked and lost carry no accumulated bonus
  -- (what ride.Unlock and ride.Lost do in the domain). Both arrival paths —
  -- player unlock and the system auto-unlock at close — inherit this rule
  -- from one place; it also freezes acc at zero on those states.
  IF NEW.state IN ('unlocked', 'lost') AND NEW.bonus_acc <> 0 THEN
    RAISE EXCEPTION 'ride_arrival_requires_zero_acc'
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;
