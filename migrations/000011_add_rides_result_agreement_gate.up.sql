-- Result agreement for the ride state gate (ADR-018/ADR-020): the last
-- missing half of rides_state_gate, landing with ResolveMatch (Phase 04).
--
-- New check 5: a ride may only move locked → won_pending|lost when the
-- match carries scores and the destination agrees with them — won_pending
-- requires the ride's team to have won, lost requires it not to have won.
-- The match is the single source of truth (ADR-018), so no writer (app,
-- script, a future refactor of the resolution query) may disagree with it.
--
-- Two deliberate design points:
--
-- 1. The trigger reads `matches` inside the caller's transaction. The
--    ResolveMatch transaction writes the match scores BEFORE updating the
--    rides, so the gate sees the fresh, uncommitted scores of its own tx.
--    If a writer updates rides without scores in place, the read yields
--    NULL and the gate refuses — the sequencing "scores first" is encoded
--    here, fail-closed.
-- 2. The check fires only on OLD.state = 'locked' AND NEW.state in the
--    result states, i.e. only on the actual transition. A state-preserving
--    rewrite of a resolved ride and a re-resolution (which matches 0 rows
--    because the rides are no longer locked) both fall through — the gate
--    must not turn ResolveMatch's idempotent replay into an error.
--
-- Draw rule: a drawn match crowns no winner (the same rule Match.Winner
-- encodes in Go), so won_pending is refused and lost is permitted — every
-- ride of a drawn match lands lost.
--
-- Refusals keep the house convention: P0001, so the store maps them to
-- ErrInvalidTransition (409) like every other gate refusal.

CREATE OR REPLACE FUNCTION rides_block_illegal_transition() RETURNS trigger AS $$
DECLARE
  v_home_score smallint;
  v_away_score smallint;
  v_home_team_id bigint;
  v_away_team_id bigint;
  v_team_won boolean;
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

  -- 5. Result agreement: locked → won_pending|lost only when the match
  -- carries scores and the destination agrees with them (ADR-018). Reads
  -- the match row in the same tx as the resolution, so it sees the scores
  -- ResolveMatch wrote earlier in the transaction — and refuses when they
  -- are not there. NEW.team_id needs no re-read of the ride.
  IF OLD.state = 'locked' AND NEW.state IN ('won_pending', 'lost') THEN
    SELECT m.home_score, m.away_score, m.home_team_id, m.away_team_id
      INTO v_home_score, v_away_score, v_home_team_id, v_away_team_id
      FROM matches m
     WHERE m.id = NEW.match_id;

    IF v_home_score IS NULL OR v_away_score IS NULL THEN
      RAISE EXCEPTION 'ride_result_scores_missing'
        USING ERRCODE = 'P0001';
    END IF;

    v_team_won := (NEW.team_id = v_home_team_id AND v_home_score > v_away_score)
               OR (NEW.team_id = v_away_team_id AND v_away_score > v_home_score);

    IF (NEW.state = 'won_pending') <> v_team_won THEN
      RAISE EXCEPTION 'ride_result_disagreement'
        USING ERRCODE = 'P0001';
    END IF;
  END IF;

  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- No trigger re-creation: rides_state_gate is still attached to the
-- replaced function, so the new body takes effect immediately.
