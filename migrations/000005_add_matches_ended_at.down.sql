ALTER TABLE matches DROP CONSTRAINT IF EXISTS matches_ended_at_check;
ALTER TABLE matches DROP COLUMN IF EXISTS ended_at;
