-- Restore the 000011/000013 body verbatim (checks 1–3, check 4 as a
-- comment, check 5 reading matches — the pre-Phase-D shape). The matches
-- reference is schema-qualified because 000015 moved the table to the
-- schedule schema (this down runs after 000015 on any downward walk).

CREATE OR REPLACE FUNCTION game.rides_block_illegal_transition() RETURNS trigger AS $$
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

  -- 4. (moved) Money-state coupling left the trigger in 000013: the
  -- predicate was row-local — NEW.state IN ('unlocked','lost') AND
  -- NEW.bonus_acc <> 0 — so the declarative constraint
  -- rides_money_state_coupling expresses it above the trigger.

  -- 5. Result agreement: locked → won_pending|lost only when the match
  -- carries scores and the destination agrees with them (ADR-018). Reads
  -- the match row in the same tx as the resolution, so it sees the scores
  -- ResolveMatch wrote earlier in the transaction — and refuses when they
  -- are not there.
  IF OLD.state = 'locked' AND NEW.state IN ('won_pending', 'lost') THEN
    SELECT m.home_score, m.away_score, m.home_team_id, m.away_team_id
      INTO v_home_score, v_away_score, v_home_team_id, v_away_team_id
      FROM schedule.matches m
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
