-- Odds precision (ADR-021): the scale chain makes odds a 2-dp operand —
-- bonus_acc scale 4 = tokens(0) + odds(2) + streakRate budget(2), exact,
-- zero rounding. numeric(6,3) permitted a third decimal place, which would
-- silently round inside bonus_acc arithmetic. Narrowing to numeric(4, 2)
-- moves the contract into the type: a 3-dp odds value is now
-- unrepresentable, not rounded on arrival.
--
-- Verified lossless before shipping: 0 rows where
-- odds <> round(odds, 2) in either database (dev and test seeds). The
-- engine spec is silent on odds precision, so the bookmaker 2-dp
-- convention is owned by this migration + ADR-021.
--
-- Watch the headroom: precision 4 = 2 integer digits, so odds cap at
-- 99.99. Widening precision (integer digits) later is the lossless
-- direction; the scale is the half coupled to ADR-021 and must not change
-- without re-deriving the chain.

ALTER TABLE matches
  ALTER COLUMN home_odds TYPE numeric(4, 2),
  ALTER COLUMN away_odds TYPE numeric(4, 2);
