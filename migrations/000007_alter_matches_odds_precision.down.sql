-- Reverse of the odds narrowing is pure widening (2 dp fit inside 3 dp),
-- which is always lossless. The numeric(6, 3) shape exists only in
-- history: re-widening the scale would re-open the rounding hole ADR-021
-- closed, so treat this down path as a rollback, not a target.

ALTER TABLE matches
  ALTER COLUMN home_odds TYPE numeric(6, 3),
  ALTER COLUMN away_odds TYPE numeric(6, 3);
