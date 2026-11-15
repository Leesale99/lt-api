-- The ended_at stamp respects a supplied end time (misalignment report
-- §6 item 1, R-SCHEDULE-008).
--
-- 000005's trigger unconditionally overwrote NEW.ended_at with now() on the
-- transition into closed. The resolve flow supplies the match's actual end
-- time in that same statement (MatchRepository.ResolveTx writes ended_at =
-- $3), so the DB discarded the value the service had just validated
-- (R-SCHEDULE-020 refuses a future end), the committed row held now() while
-- the response and MatchResultRecorded echoed the request, and
-- TestResolveMatchHandler locked that discrepancy in.
--
-- Resolved semantics: the request's ended_at is the match's actual end time
-- — a client-supplied fact about the real world. The database's job (this
-- rule) is to stamp it exactly once, on the single transition into closed,
-- and never rewrite it afterwards — not to substitute its own clock. So on
-- that transition the row keeps the supplied value when one is present; when
-- a writer supplies none, the DB stamps now() (the DB-authority backstop for
-- direct writers that do not know the fact). the CHECK matches_ended_at_check
-- (000005: ended_at after starts_at, non-null exactly when closed) still
-- validates whichever value survives.
--
-- Only the body changes; the trigger keeps pointing at this function
-- (000019's pattern). The schema qualification and the `SET search_path =
-- schedule` pin re-state 000015's function-level setting, because CREATE OR
-- REPLACE replaces the whole definition, settings included.
CREATE OR REPLACE FUNCTION schedule.matches_set_ended_at_on_close() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
BEGIN
  -- ended_at is set exactly once: on the transition into 'closed'. A value
  -- supplied on that transition is the caller's observation of when the
  -- match actually ended — the resolve flow supplies it and R-SCHEDULE-020
  -- validates it — so it is kept. Only when no value is supplied does the
  -- database stamp its own clock (the backstop for direct writers that do
  -- not know the fact). Edits to an already-closed match must not rewrite
  -- this fact.
  IF NEW.status = 'closed' AND OLD.status IS DISTINCT FROM 'closed' AND NEW.ended_at IS NULL THEN
    NEW.ended_at := now();
  END IF;
  RETURN NEW;
END $$;
