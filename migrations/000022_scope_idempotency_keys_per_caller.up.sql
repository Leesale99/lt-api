-- Idempotency keys are per caller (R-KERNEL-003): the claim row becomes
-- (key, user_id) instead of the bare key text.
--
-- Why: 000010's primary key was the key text alone, one global namespace, so
-- two clients that happened to pick the same key collided — the second was
-- refused 409 as key misuse even though it named its own request. Ride routes
-- have carried a permission gate since Phase G, so every ride command now has
-- an authenticated principal to scope its claim to (misalignment-report §6
-- item 13).
--
-- The shape:
--   - user_id is the authenticated caller's id. 0 is reserved for the
--     system caller: the settlement handlers claim an EventID with no request
--     principal behind it (users.id is serial, so no identity row can be 0).
--   - legacy rows from 000010 predate caller scoping and are carried over as
--     user_id = 0. That keeps the settlement events' own dedup intact across
--     the deploy (those rows were mostly event claims); a ride retry that was
--     in flight under the old namespace executes once more instead of
--     replaying, which is the only honest reading of a claim that never named
--     its caller.
--   - the column keeps NO default after the swap: a claim that forgets the
--     caller must fail loudly, not silently fall back into one global
--     namespace.
--
-- No table rebuild: the composite key is a constraint swap on the existing
-- table, which keeps the table's owner, its per-context GRANTs (000018) and
-- the CHECK constraints 000010 named (idempotency_keys_key_check is cited by
-- R-KERNEL-001). A rebuild would have to re-grant and re-name all of it.
ALTER TABLE game.idempotency_keys
  ADD COLUMN user_id integer NOT NULL DEFAULT 0
    CONSTRAINT idempotency_keys_user_id_check CHECK (user_id >= 0);

ALTER TABLE game.idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE game.idempotency_keys
  ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (key, user_id);

ALTER TABLE game.idempotency_keys ALTER COLUMN user_id DROP DEFAULT;

-- Retention (R-KERNEL-003): the claim sweeps rows older than a day on its own
-- transaction, so the age predicate is a query shape, not a constraint. The
-- index keeps that sweep (and a future worker's) off a sequential scan as the
-- table grows.
CREATE INDEX idempotency_keys_created_at_idx ON game.idempotency_keys (created_at);
