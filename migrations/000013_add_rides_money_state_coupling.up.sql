-- The money-state coupling changes enforcement home, not content. What
-- trigger check 4 of 000011 refused — unlocked|lost arrivals carrying
-- accumulated bonus_acc — becomes the declarative constraint
-- rides_money_state_coupling. The hierarchy from the logic-placement
-- research ranks a declarative constraint above a trigger whenever the
-- constraint can express the rule, and this predicate is row-local: it
-- references only NEW, which is exactly what a CHECK is evaluated against
-- on UPDATE and INSERT alike. Checks 1–3 need OLD (a transition needs the
-- previous row), check 5 reads matches — those stay in the trigger and
-- keep their numbers (the store comments and the score→state fixture name
-- "check 5"; renumbering would orphan those references).
--
-- Burn is deliberately outside the predicate: bonus_acc is fulfilled as
-- TB and the column keeps its accrued value (ride.Burn in the domain
-- leaves acc untouched; rideAutoBurnSQL in rounds.go banks on the same
-- freedom).
--
-- The refusal surface changes shape: P0001
-- ('ride_arrival_requires_zero_acc') becomes 23514 carrying the
-- constraint name rides_money_state_coupling. The store maps it to
-- ErrInvalidTransition (409) by constraint name — the state machine
-- refused the destination, the same answer the trigger's refusal
-- produced — while the other rides CHECKs (state, tokens_locked,
-- base_at_lock, bonus_acc, streak) keep falling through unhandled.
--
-- On INSERT the constraint is unreachable backup: 000012's insert gate
-- refuses every insert but locked, and locked rows are not coupled to
-- zero acc. The rule's live path is the UPDATE half, where check 4 sat.
--
-- Change-detector (money-state coupling): the domain copies are
-- ride.Unlock and ride.Lost (internal/game/rides.go — both zero the acc),
-- the persistence copy is rideAutoUnlockSQL's bonus_acc = 0
-- (internal/game/rounds.go); this constraint is the enforcement home. The
-- DB-side probe is TestRideMoneyStateCoupling
-- (internal/game/constraints_test.go). Change one, change all.
ALTER TABLE rides
  ADD CONSTRAINT rides_money_state_coupling
  CHECK (state NOT IN ('unlocked', 'lost') OR bonus_acc = 0);

-- Trigger body without check 4: the horizontally-adjacent rules stay
-- verbatim (checks 1–3 and 5 carry over from 000011 unchanged).
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

  -- 4. (moved) Money-state coupling left the trigger in 000013: the
  -- predicate was row-local — NEW.state IN ('unlocked','lost') AND
  -- NEW.bonus_acc <> 0 — so the declarative constraint
  -- rides_money_state_coupling expresses it above the trigger.
  -- unnumbered here on purpose: check 5 keeps its name everywhere.

  -- 5. Result agreement: locked → won_pending|lost only when the match
  -- carries scores and the destination agrees with them (ADR-018). Reads
  -- the match row in the same tx as the resolution, so it sees the scores
  -- ResolveMatch wrote earlier in the transaction — and refuses when they
  -- are not there. NEW.team_id needs no re-read of the ride.
  --
  -- Score→state rule site 3 of 3 (the enforcement home): the Go copy is
  -- Match.Winner (internal/game/matches.go), the computation copy is the
  -- CASE in RideStore.UpdateAllForMatchTx (internal/game/rides.go); tied
  -- by the shared fixture in internal/game/score_state_rule_test.go —
  -- change one, change all three.
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
