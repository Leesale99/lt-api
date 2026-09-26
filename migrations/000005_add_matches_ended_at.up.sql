ALTER TABLE matches ADD COLUMN ended_at timestamp(0) with time zone NULL;

UPDATE matches set ended_at = starts_at + interval '2 hours' WHERE status = 'closed';

ALTER TABLE matches ADD CONSTRAINT matches_ended_at_check CHECK (
    (status = 'closed') = (ended_at IS NOT NULL)
    AND (ended_at IS NULL OR ended_at > starts_at)
  );

CREATE FUNCTION matches_set_ended_at_on_close() RETURNS trigger AS $$
  BEGIN
    -- ended_at is set exactly once: on the transition into 'closed'. Edits to
    -- an already-closed match must not rewrite this observed fact.
    IF NEW.status = 'closed' AND OLD.status IS DISTINCT FROM 'closed' THEN
      NEW.ended_at := now();
    END IF;
    RETURN NEW;
  END $$ LANGUAGE plpgsql;

CREATE TRIGGER matches_set_ended_at_on_close
  BEFORE UPDATE ON matches
  FOR EACH ROW EXECUTE FUNCTION matches_set_ended_at_on_close();
