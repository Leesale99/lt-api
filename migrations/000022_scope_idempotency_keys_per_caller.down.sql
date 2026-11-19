-- Back to one global key namespace (the shape 000010 created).
--
-- The composite primary key has to go first, and a key text that two callers
-- claimed concurrently is two rows: collapse each key to its most recent claim
-- (the tuple comparison keeps one row per key deterministically) before the
-- single-column key can be unique again.
DELETE FROM game.idempotency_keys a
USING game.idempotency_keys b
WHERE a.key = b.key
  AND (a.created_at, a.user_id) < (b.created_at, b.user_id);

DROP INDEX IF EXISTS game.idempotency_keys_created_at_idx;

ALTER TABLE game.idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE game.idempotency_keys
  ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (key);

ALTER TABLE game.idempotency_keys
  DROP CONSTRAINT idempotency_keys_user_id_check;
ALTER TABLE game.idempotency_keys DROP COLUMN user_id;
