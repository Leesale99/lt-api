-- Round lifecycle gates (ADR-007/008 pattern: the DB is authoritative for
-- lifecycle invariants, app checks are advisory UX). Two gates, one
-- migration, because the round-close transaction's season flip (ADR-022) is
-- only as sound as the sequencing both encode.
--
-- 1. rounds_progress_gate — a round may only move forward (created → open,
--    created → closed, open → closed) when:
--      a. its season is live (open or in_progress). The season flip in
--         RoundStore.Open no-ops on status <> open, so without this rule an
--         open round under a created season is reachable; decided 2026-10-01
--         that it must be refused.
--      b. the round sequence is unbroken: the highest closed round of the
--         season must be exactly number - 1 (COALESCE makes round 1 the
--         entry point of a fresh season). This is what makes the
--         season-flip-on-38 sound: 38 can only close after 1..37 closed.
--    Covered for INSERT too (status beyond created), and for renumbering a
--    live or closed round: without those, a writer could bypass sequencing
--    by inserting a pre-opened round or renaming its way around the order.
--    Regressions (closed → open, → created) are NOT gated here — they belong
--    to rounds_freeze_gate (started matches), which already refuses them.
--    Continuation stays compatible: a locked ride re-points to a match of
--    the next round while that round is still created (NextForTeam filters
--    r.status <> 'closed', it does not require the destination open).
--
-- 2. rounds_close_gate — closing a round requires every match of the round
--    closed (transitively no locked stragglers once ResolveMatch lands in
--    Phase 04) and no ride still in won_pending (undecided rides are
--    auto-resolved by the round-close transaction before the flip, ADR-022;
--    this gate is the backstop for writers that skip it). Rides reach the
--    round through matches (no round_id on rides, ADR-018).
--
-- Both raise P0001; the store maps rounds-gate refusals to ErrRecordInUse
-- (409), like the freeze gates.

CREATE FUNCTION rounds_block_illegal_progress() RETURNS trigger AS $$
BEGIN
  -- Fire on forward transitions (created → open/closed, open → closed), on
  -- inserts beyond created, and on renumbering a live or closed round. A
  -- state-preserving rewrite and a regression are not progress.
  IF NOT (
    NEW.status IN ('open', 'closed')
    AND (TG_OP = 'INSERT'
         OR (OLD.status = 'created' AND NEW.status IN ('open', 'closed'))
         OR (OLD.status = 'open' AND NEW.status = 'closed')
         OR OLD.number IS DISTINCT FROM NEW.number)
  ) THEN
    RETURN NEW;
  END IF;

  -- The parent season must be live: no round of a season goes live (or
  -- becomes history) while the season itself is created or closed.
  IF NOT EXISTS (
    SELECT 1 FROM seasons s
    WHERE s.id = NEW.season_id AND s.status IN ('open', 'in_progress')
  ) THEN
    RAISE EXCEPTION 'round_progress_season_not_live'
      USING ERRCODE = 'P0001';
  END IF;

  -- The sequence is unbroken: the highest closed round is exactly the
  -- predecessor. Round 1 is the entry point (max over no closed rounds = 0).
  IF COALESCE((
    SELECT max(r.number) FROM rounds r
    WHERE r.season_id = NEW.season_id AND r.status = 'closed'
  ), 0) <> NEW.number - 1 THEN
    RAISE EXCEPTION 'round_sequence_incomplete'
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION rounds_block_illegal_close() RETURNS trigger AS $$
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

CREATE TRIGGER rounds_progress_gate
  BEFORE INSERT OR UPDATE ON rounds
  FOR EACH ROW EXECUTE FUNCTION rounds_block_illegal_progress();

CREATE TRIGGER rounds_close_gate
  BEFORE UPDATE ON rounds
  FOR EACH ROW EXECUTE FUNCTION rounds_block_illegal_close();
