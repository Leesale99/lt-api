CREATE TABLE IF NOT EXISTS user_tokens (
  hash bytea PRIMARY KEY,
  user_id bigint NOT NULL
    CONSTRAINT user_tokens_user_id_fkey REFERENCES users ON DELETE CASCADE,
  expiry timestamp(0) with time zone NOT NULL,
  scope text NOT NULL
);
