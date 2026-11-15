-- Restore the pre-000019 gate bodies: the freeze gates keep only their
-- started-regression rule (000001) and rounds_progress_gate only its forward
-- progress rule (000008) — the status-rank half is dropped, so "closed is
-- final" goes back to being an app policy rather than a DB invariant.
--
-- The bodies below are copied verbatim from 000001/000008 as they exist in
-- the tree today; the only addition is `SET search_path = schedule`, which
-- re-states the function-level pin 000015 applied (CREATE OR REPLACE
-- replaces the whole function definition, settings included). Trigger
-- definitions are untouched: they keep pointing at these functions.

CREATE OR REPLACE FUNCTION schedule.matches_block_started_regression() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
BEGIN
  -- A started match cannot return to created and cannot be postponed
  -- (postponement is the only status that could move time backwards).
  -- Keyed on OLD.starts_at: moving starts_at forward in the same statement
  -- cannot un-start a match.
  IF NEW.status IN ('created', 'postponed') AND OLD.starts_at <= now() THEN
    RAISE EXCEPTION 'started_match_blocks_status_regression'
      USING ERRCODE = 'P0001';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION schedule.rounds_block_illegal_progress() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
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
END $$;

CREATE OR REPLACE FUNCTION schedule.seasons_block_started_regression() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
BEGIN
  IF NEW.status IN ('created', 'open') AND season_has_started_match(OLD.id) THEN
    RAISE EXCEPTION 'started_match_blocks_season_regression'
      USING ERRCODE = 'P0001';
  END IF;
  RETURN NEW;
END $$;
