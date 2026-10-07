-- Idempotency keys (ADR-024): request dedup at the HTTP boundary.
--
--   - one row per claimed Idempotency-Key; the claim is a conditional
--     INSERT (ON CONFLICT DO NOTHING) inside the command's transaction
--   - response_status/response_body are the stored HTTP answer a retry
--     replays byte-identical; the claim row starts as an empty placeholder
--     and is filled by the same transaction that executes the command, so
--     a committed row is always a completed response and a failed command
--     rolls its claim back (failures are never cached)
--   - response_body is bytea, NOT jsonb: jsonb normalization (whitespace,
--     key order) would break the byte-identical replay promise
--   - request_hash (SHA-256, 32 bytes) covers method + request URI + body:
--     the same key re-used with a different request is refused (409)
--   - no player scoping yet: the lock route has no authenticated player
--     binding; revisit when ownership lands on ride commands
--   - no retention policy yet: rows live until a worker phase cleans them
CREATE TABLE idempotency_keys (
  key text PRIMARY KEY
    CONSTRAINT idempotency_keys_key_check CHECK (key <> '' AND octet_length(key) <= 255),
  endpoint text NOT NULL
    CONSTRAINT idempotency_keys_endpoint_check CHECK (endpoint <> ''),
  request_hash bytea NOT NULL
    CONSTRAINT idempotency_keys_request_hash_check CHECK (octet_length(request_hash) = 32),
  response_status integer NOT NULL,
  response_body bytea NOT NULL,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now()
);
