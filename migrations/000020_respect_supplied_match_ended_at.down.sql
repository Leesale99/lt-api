-- Reverse of 000020: restore 000005's stamp body, which overwrites whatever
-- ended_at the closing statement supplied with now(). Copied verbatim from
-- migrations/000005_add_matches_ended_at.up.sql; the only additions are the
-- schema qualification and `SET search_path = schedule`, which re-state
-- 000015's function-level pin (CREATE OR REPLACE replaces the whole
-- definition, settings included). The trigger definition is untouched: it
-- keeps pointing at this function.
CREATE OR REPLACE FUNCTION schedule.matches_set_ended_at_on_close() RETURNS trigger
LANGUAGE plpgsql
SET search_path = schedule
AS $$
  BEGIN
    -- ended_at is set exactly once: on the transition into 'closed'. Edits to
    -- an already-closed match must not rewrite this observed fact.
    IF NEW.status = 'closed' AND OLD.status IS DISTINCT FROM 'closed' THEN
      NEW.ended_at := now();
    END IF;
    RETURN NEW;
  END $$;
