CREATE TABLE rides (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  player_id bigint NOT NULL
    CONSTRAINT rides_player_id_fkey REFERENCES players ON DELETE RESTRICT,
  team_id bigint NOT NULL
    CONSTRAINT rides_team_id_fkey REFERENCES teams ON DELETE RESTRICT,
  match_id bigint NOT NULL
    CONSTRAINT rides_match_id_fkey REFERENCES matches ON DELETE RESTRICT,
  -- No DEFAULT: the initial-state contract (ADR-019, rides are created
  -- locked) is owned by the ride.Create domain command. A default would
  -- silently "fix" a caller that skips Create; NOT NULL makes the omission
  -- fail loudly instead.
  state text NOT NULL
    CONSTRAINT rides_state_check CHECK (state IN ('locked', 'won_pending', 'burned', 'unlocked', 'lost')),
  -- Money precision (ADR-021): tokens are whole units — bigint makes
  -- fractional tokens unrepresentable; bonus_acc scale = tokens(0) +
  -- odds(2) + streakRate budget(2, declared ≤2-dp contract), so the whole
  -- economy stores exactly, zero rounding.
  tokens_locked bigint NOT NULL
    CONSTRAINT rides_tokens_locked_check CHECK (tokens_locked > 0),
  base_at_lock numeric(18, 2) NOT NULL
    CONSTRAINT rides_base_at_lock_check CHECK (base_at_lock > 0),
  bonus_acc numeric(18, 4) NOT NULL DEFAULT 0
    CONSTRAINT rides_bonus_acc_check CHECK (bonus_acc >= 0),
  streak integer NOT NULL DEFAULT 0
    CONSTRAINT rides_streak_check CHECK (streak >= 0),
  version integer NOT NULL DEFAULT 1
);

-- FK indexes (house convention — Postgres does not auto-index the
-- referencing side). rides_match_id_idx also serves the RESTRICT
-- enforcement query the FK machinery runs on match deletes, and the
-- match-result ride resolution planned for Phase 04; player/team indexes
-- serve the GetAll filters.
CREATE INDEX rides_match_id_idx ON rides (match_id);
CREATE INDEX rides_player_id_idx ON rides (player_id);
CREATE INDEX rides_team_id_idx ON rides (team_id);

-- State gate (ADR-020): the phase-free half of the ADR-016 transition
-- matrix, DB-enforced so no writer (app, script, the round-close
-- transaction) can corrupt ride state. Phase-precise checks and
-- result-agreement (win/loss must agree with match scores) land with
-- ResolveMatch (Phase 04) — they need the match-resolution operation to
-- exist first. The store maps P0001 here to ErrInvalidTransition, so an
-- app/DB disagreement surfaces as the invalid-state 409, never silently.
CREATE FUNCTION rides_block_illegal_transition() RETURNS trigger AS $$
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

CREATE TRIGGER rides_state_gate
  BEFORE UPDATE ON rides
  FOR EACH ROW EXECUTE FUNCTION rides_block_illegal_transition();
