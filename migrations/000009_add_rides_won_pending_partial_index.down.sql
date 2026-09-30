-- Dropping the index returns the round-close statements to rides seq
-- scans (the measured before-shapes in the up migration comment). Safe:
-- the index serves performance only, no invariant lives in it.
DROP INDEX rides_won_pending_match_id_idx;
