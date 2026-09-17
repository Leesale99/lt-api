CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  name text NOT NULL, 
  email citext UNIQUE NOT NULL, 
  password_hash bytea NOT NULL,
  activated bool NOT NULL,
  version integer NOT NULL DEFAULT 1
);
