-- Status-rank gates: "closed is final" becomes a DB invariant
-- (misalignment-report §6 item 7, R-SCHEDULE-003).
--
-- Before this migration the freeze gates refused only the *started*
-- regressions of 000001, and rounds_progress_gate (000008) refused only
-- forward progress — its header said regressions were "NOT gated here".
-- So a direct writer (psql, a backfill, a new store method) could move a
-- closed match back to in_progress, a closed round with no started matches
-- back to open, or a live season back a stage. The rank validators in
-- internal/schedule/internal/model (matchStatusRank / roundStatusRank /
-- seasonStatusRank) refused those transitions for the API path only, and
-- app checks are advisory UX: the DB owns lifecycle invariants (N writers,
-- source-of-truth lesson).
--
-- This migration adds the rank half to the three gate functions, mirroring
-- those Go maps exactly — regression = newRank < oldRank:
--
--   matches:  created 0, postponed 0, in_progress 1, closed 2
--   rounds:   created 0, open 1, closed 2
--   seasons:  created 0, open 1, in_progress 2, closed 3
--
-- The Go maps are the authority this DB copy must not drift from. The
-- drift detector TestStatusRankGatesMirrorGoRanks (internal/schedule/
-- internal/repository/constraints_test.go) probes every ordered status pair
-- against a fresh fixture through model.MatchStatusRegresses /
-- RoundStatusRegresses / SeasonStatusRegresses and pins the two verdicts
-- together — change one, change both.
--
-- Scope and preserved flows:
--   - UPDATE only. Each freeze trigger is BEFORE UPDATE; rounds_progress_gate
--     also fires on INSERT and skips the rank half there (a fresh row has no
--     earlier stage). The vocabulary drift test INSERTs every status value
--     and stays green.
--   - Legal flows are unchanged: created→in_progress→closed,
--     created↔postponed (same rank, pre-start), postponed→in_progress,
--     created→open→closed rounds, same-status rewrites (incl. closed→closed),
--     the resolve path's in_progress→closed and the edit path's transitions.
--   - 000008's created-round renumbering stays allowed: the rank rule reads
--     status only, never number (§6 item 5 stays out of scope — the gate must
--     not import that over-strictness).
--
-- Refusals raise P0001 like every other gate, with distinctive messages so a
-- rank refusal is told apart from the started-freeze refusals:
-- match_status_regression / round_status_regression /
-- season_status_regression. Stores map P0001 to ErrRecordInUse (409).
--
-- No trigger re-creation: the triggers still point at these functions, so
-- the new bodies take effect immediately (000014's pattern). Each definition
-- re-states 000015's function-level search_path pin, because CREATE OR
-- REPLACE replaces the whole function definition, settings included.

CREATE OR REPLACE FUNCTION schedule.matches_block_started_regression() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
DECLARE
  old_rank integer;
  new_rank integer;
BEGIN
  -- Rank mirror of model.matchStatusRank (internal/schedule/internal/model/
  -- match.go): created and postponed share rank 0, in_progress 1, closed 2.
  -- Checked first so the refusal message is deterministic: on a started row
  -- both rules can refuse (in_progress → created/postponed, closed → any
  -- earlier stage), and the rank verdict is the one that says why.
  old_rank := CASE OLD.status
                WHEN 'created'     THEN 0
                WHEN 'postponed'   THEN 0
                WHEN 'in_progress' THEN 1
                WHEN 'closed'      THEN 2
              END;
  new_rank := CASE NEW.status
                WHEN 'created'     THEN 0
                WHEN 'postponed'   THEN 0
                WHEN 'in_progress' THEN 1
                WHEN 'closed'      THEN 2
              END;

  IF old_rank IS NOT NULL AND new_rank IS NOT NULL AND new_rank < old_rank THEN
    RAISE EXCEPTION 'match_status_regression'
      USING ERRCODE = 'P0001';
  END IF;

  -- Unchanged 000001 rule: a started match cannot return to created and
  -- cannot be postponed (postponement is the only status that could move
  -- time backwards). Keyed on OLD.starts_at: moving starts_at forward in the
  -- same statement cannot un-start a match.
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
DECLARE
  old_rank integer;
  new_rank integer;
BEGIN
  -- Rank mirror of model.roundStatusRank (round.go): created 0, open 1,
  -- closed 2. UPDATE only — this trigger also guards INSERT, where there is
  -- no earlier stage to regress from. The check runs BEFORE the early return
  -- below: that early return is exactly where regressions used to pass
  -- through ("a state-preserving rewrite and a regression are not progress").
  IF TG_OP = 'UPDATE' THEN
    old_rank := CASE OLD.status
                  WHEN 'created' THEN 0
                  WHEN 'open'    THEN 1
                  WHEN 'closed'  THEN 2
                END;
    new_rank := CASE NEW.status
                  WHEN 'created' THEN 0
                  WHEN 'open'    THEN 1
                  WHEN 'closed'  THEN 2
                END;

    IF old_rank IS NOT NULL AND new_rank IS NOT NULL AND new_rank < old_rank THEN
      RAISE EXCEPTION 'round_status_regression'
        USING ERRCODE = 'P0001';
    END IF;
  END IF;

  -- Everything below is the 000008 body, unchanged: fire on forward
  -- transitions (created → open/closed, open → closed), on inserts beyond
  -- created, and on renumbering a live or closed round.
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
    SELECT 1 FROM schedule.seasons s
    WHERE s.id = NEW.season_id AND s.status IN ('open', 'in_progress')
  ) THEN
    RAISE EXCEPTION 'round_progress_season_not_live'
      USING ERRCODE = 'P0001';
  END IF;

  -- The sequence is unbroken: the highest closed round is exactly the
  -- predecessor. Round 1 is the entry point (max over no closed rounds = 0).
  IF COALESCE((
    SELECT max(r.number) FROM schedule.rounds r
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
DECLARE
  old_rank integer;
  new_rank integer;
BEGIN
  -- Rank mirror of model.seasonStatusRank (season.go): created 0, open 1,
  -- in_progress 2, closed 3. Checked first, so a started season moving back
  -- reports the rank refusal deterministically.
  old_rank := CASE OLD.status
                WHEN 'created'     THEN 0
                WHEN 'open'        THEN 1
                WHEN 'in_progress' THEN 2
                WHEN 'closed'      THEN 3
              END;
  new_rank := CASE NEW.status
                WHEN 'created'     THEN 0
                WHEN 'open'        THEN 1
                WHEN 'in_progress' THEN 2
                WHEN 'closed'      THEN 3
              END;

  IF old_rank IS NOT NULL AND new_rank IS NOT NULL AND new_rank < old_rank THEN
    RAISE EXCEPTION 'season_status_regression'
      USING ERRCODE = 'P0001';
  END IF;

  -- Unchanged 000001 rule: once a match of the season has started, no
  -- created/open return.
  IF NEW.status IN ('created', 'open') AND schedule.season_has_started_match(OLD.id) THEN
    RAISE EXCEPTION 'started_match_blocks_season_regression'
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END $$;
