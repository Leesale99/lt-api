-- Phase D: result agreement (000011, check 5) changes enforcement home.
-- The trigger body reads `matches` — schedule's table — inside the
-- writing transaction; after the split that is the cross-context read the
-- boundary rule (ADR-028) removes from the DB. The check moves into game
-- Go code at the ride-result mutation paths (the settlement handlers:
-- OnMatchResultRecorded / OnRoundCompleted — the only paths a locked ride
-- can arrive won_pending|lost from), verified against the committed match
-- result through the SchedulePort (Store.MatchResultTx):
--
--   - scores missing on the committed match → refused
--     (ride_result_scores_missing);
--   - destination disagreeing with the committed result → refused
--     (ride_result_disagreement);
--   - refusal surface: ErrInvalidTransition — the same 409 the store
--     mapped the P0001 refusals to (store.IsTriggerViolation), so no Go
--     error semantics change.
--
-- The refusal happens BEFORE the arrival write inside the same
-- transaction, so a lying payload rolls the whole settlement back — the
-- tx-atomic refusal the trigger used to provide, minus the cross-table
-- read. DB CHECK/state-graph enforcement (checks 1–3 below,
-- rides_money_state_coupling, the insert gate) is untouched.
--
-- checks 1–3 stay in the trigger verbatim: they are row-local (OLD/NEW
-- only) and therefore inside the game context's own state machine. Check
-- 4 left the body in 000013 (rides_money_state_coupling CHECK) and keeps
-- its place in the numbering.

CREATE OR REPLACE FUNCTION game.rides_block_illegal_transition() RETURNS trigger AS $$
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

  -- 4. (moved in 000013) Money-state coupling is the declarative CHECK
  -- rides_money_state_coupling; not in this body.

  -- 5. (moved in 000017) Result agreement is game Go code at the
  -- settlement handlers (internal/game/events.go), reading the committed
  -- match result through the SchedulePort — the trigger body's read of
  -- `matches` was the cross-context coupling this split removes. The Go
  -- check keeps the same two refusals: ride_result_scores_missing (the
  -- committed match carries no scores) and ride_result_disagreement (the
  -- destination contradicts the committed result). Score→state rule site
  -- 3 of 3 is now the Go check (internal/game/events.go); tied by the
  -- shared fixture in internal/game/score_state_rule_test.go — change
  -- one, change all three.

  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- No trigger re-creation: rides_state_gate is still attached to the
-- replaced function, so the new body takes effect immediately.
