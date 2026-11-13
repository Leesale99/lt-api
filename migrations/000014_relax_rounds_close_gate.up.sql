-- Phase C of the Game/Schedule split: round-close becomes EVENTUAL (ADR-027
-- event seam). Schedule closes a round on feed completion and publishes
-- RoundCompleted; the game context settles the round's undecided rides
-- afterwards (auto-unlock rounds 1-37, auto-burn round 38), inside its own
-- transaction, driven by the event payload.
--
-- That removes the premise of rounds_close_gate's second check: it refused
-- any close over rides still in won_pending because the close transaction
-- itself auto-resolved them just before the flip (ADR-022) and the check was
-- the backstop for writers that skipped that resolution. With the
-- resolution gone from the close, the backstop contradicts the design: the
-- undecided rides exist exactly because the settlement is now a separate,
-- later transaction. This migration drops ONLY that check; everything else
-- in rounds_close_gate (every match of the round closed) and in
-- rounds_progress_gate (live season, unbroken sequence) is untouched.
--
-- What protects undecided rides now:
--   - the game context's settlement handler claims the event ID first
--     (internal/idempotency), so the RoundCompleted work runs exactly once;
--   - each ride arrival is a state-guarded CAS write (the DB transition
--     graph in rides_state_gate stays authoritative), so replays and races
--     are no-ops;
--   - a crash between the close's commit and the settlement (or a failed
--     settlement) leaves undecided rides in a closed round — the accepted
--     Phase C gap, swept by the Phase 07 background sweep / replaced by the
--     Phase 08 transactional outbox. No money is lost: the rides simply
--     keep waiting, and rides_state_gate still refuses every illegal
--     arrival.
--
-- Down migration restores the pre-Phase-C backstop verbatim.

CREATE OR REPLACE FUNCTION rounds_block_illegal_close() RETURNS trigger AS $$
BEGIN
  -- Only a transition into closed is a close; a state-preserving rewrite of
  -- an already-closed round passes.
  IF NEW.status = 'closed' AND OLD.status IS DISTINCT FROM 'closed' THEN
    -- Every match must be settled: a closed round owns no live schedule.
    IF EXISTS (
      SELECT 1 FROM matches m
      WHERE m.round_id = NEW.id AND m.status <> 'closed'
    ) THEN
      RAISE EXCEPTION 'round_close_has_unclosed_matches'
        USING ERRCODE = 'P0001';
    END IF;

    -- (round_close_has_unresolved_rides removed in 000014: with the ride
    -- auto-resolution stripped from the close and moved behind the
    -- RoundCompleted event, undecided rides in a closing round are the
    -- designed intermediate state, not a corruption signal. See the
    -- migration header.)
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- No trigger re-creation: rounds_close_gate is still attached to the
-- replaced function, so the new body takes effect immediately.
