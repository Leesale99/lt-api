-- Restore the pre-event-seam rounds_close_gate: refuse closing over undecided
-- won_pending rides (round_close_has_unresolved_rides). Only meaningful
-- together with the pre-split close transaction that auto-resolved those
-- rides before the flip — see 000014 up for why it was dropped.

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

    -- No undecided rides: the close transaction auto-resolves won_pending
    -- rides before this flip (ADR-022); this backstop holds for any writer.
    IF EXISTS (
      SELECT 1 FROM rides rd
      JOIN matches m ON m.id = rd.match_id
      WHERE m.round_id = NEW.id AND rd.state = 'won_pending'
    ) THEN
      RAISE EXCEPTION 'round_close_has_unresolved_rides'
        USING ERRCODE = 'P0001';
    END IF;
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;
