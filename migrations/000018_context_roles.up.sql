-- The privilege boundary. One NOLOGIN role per context, granted
-- exactly its own schema's CRUD — after the server pools run
-- `SET ROLE <context_role>` (the post-connect hook), a cross-context query
-- fails on PERMISSION even when the name is resolvable: the boundary is
-- Postgres' to enforce, not a code-review convention.
--
--   schedule_app  full CRUD on schedule.*
--   game_app      full CRUD on game.*. The schedule side is reachable
--                 ONLY through schedule's two SECURITY DEFINER round
--                 guards (the SchedulePort's write-instant FOR SHARE
--                 gates); every schedule table is read- and write-
--                 denied to it.
--   identity_app  full CRUD on identity.*
--
-- The migration runner (CURRENT_USER — the app's own login role in the
-- common one-role deployment) is granted membership in all three; the
-- server pools SET ROLE into them, so the session's effective privileges
-- are the context role's, never the owner's. Deployments that separate
-- contexts onto different DSNs grant each login role only its context's
-- membership (see README).
--
-- The SchedulePort's write-instant round gates are the one deliberate
-- exception to "no schedule access from game": they FOR SHARE-lock
-- schedule rows INSIDE game's transaction (the serialization against a
-- concurrent close/resolve), and row locking needs UPDATE privilege —
-- granting that would let game corrupt schedule state, which is exactly
-- what the boundary exists to prevent. So the two gates are schedule's
-- own SECURITY DEFINER operations (functions below): schedule's SQL,
-- running as the schema owner, taking the same row locks inside the
-- caller's transaction, returning only the fact (status / destination
-- liveness). game_app gets EXECUTE and nothing else; even SELECT on
-- schedule.matches stays denied to it.
--
-- The function bodies the triggers execute resolve through each role's
-- search_path (set below): schedule gates live in the schedule schema and
-- read schedule tables as schedule_app. The trgm/citext extensions stay
-- in public — the per-role search_path keeps public behind its own
-- schema, which is what the citext = operator (and the trgm index
-- opclass) resolve through.
--
-- CREATE ROLE is cluster-level and this migration is applied to several
-- databases over its life (dev, per-suite test databases), so the
-- creations are guarded: an existing role is kept as-is. The runner role
-- must end up ADMIN on all three (the creator is automatically); a second
-- runner role needs the admin handover first —
--   GRANT schedule_app, game_app, identity_app TO <role> WITH ADMIN OPTION;
-- else the membership grant below refuses, and the pools' SET ROLE would
-- refuse at boot.

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'schedule_app') THEN
    CREATE ROLE schedule_app NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'game_app') THEN
    CREATE ROLE game_app NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_app') THEN
    CREATE ROLE identity_app NOLOGIN;
  END IF;
END $$;

-- SQL in the stores stays unqualified. Enforcement is the GRANTs, not
-- the path: each role's documented search_path resolves unqualified names
-- to its own schema (for a login role this applies at session start; the
-- server's pooled connections run SET ROLE from a shared login, so their
-- post-connect hook sets SET ROLE + search_path explicitly — the pool
-- wiring is where the pairing is enforced).
ALTER ROLE schedule_app SET search_path = schedule, public;
ALTER ROLE game_app SET search_path = game, public;
ALTER ROLE identity_app SET search_path = identity, public;

-- The two write-instant round gates as schedule's own definer operations
-- (see header). SECURITY DEFINER + a pinned search_path: the body's
-- schedule.* names resolve inside the schedule schema regardless of the
-- caller's path, so a caller cannot shadow them; the definer is the
-- schema owner the migration runner created the tables as.

-- round_open_guard: the ride's own round status at the write instant.
-- The FOR SHARE on the round row serializes the caller's ride write
-- against a concurrent round close. NULL when the match is unknown — the
-- caller maps that to its not-found refusal, exactly like the previous
-- statement did.
-- VOLATILE, not STABLE: the statement takes row locks (FOR SHARE), which
-- only volatile functions may do.
CREATE FUNCTION schedule.round_open_guard(match_id bigint) RETURNS text
LANGUAGE sql SECURITY DEFINER
SET search_path = schedule
AS $$
  SELECT r.status
  FROM matches m
  INNER JOIN rounds r ON r.id = m.round_id
  WHERE m.id = round_open_guard.match_id
  FOR SHARE OF r
$$;

-- destination_open_guard: a continuation destination LIVE at the write
-- instant (un-ended match, round not closed). Both rows are share-locked,
-- so a concurrent resolve of the destination or close of its round
-- serializes before the caller's ride write. FALSE covers every refusal
-- cause (unknown match, already ended, round closed) — the caller maps
-- them all to the round-refusal, like the previous statement did.
CREATE FUNCTION schedule.destination_open_guard(match_id bigint) RETURNS boolean
LANGUAGE sql SECURITY DEFINER
SET search_path = schedule
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM matches m
    INNER JOIN rounds r ON r.id = m.round_id
    WHERE m.id = destination_open_guard.match_id
      AND m.ended_at IS NULL
      AND r.status <> 'closed'
    FOR SHARE OF m, r
  )
$$;

-- EXECUTE on the two guards, and only on them: PUBLIC's default EXECUTE
-- is revoked so no other role picks the surface up by accident.
REVOKE ALL ON FUNCTION schedule.round_open_guard(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION schedule.round_open_guard(bigint) TO game_app;
REVOKE ALL ON FUNCTION schedule.destination_open_guard(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION schedule.destination_open_guard(bigint) TO game_app;

-- schedule_app: the calendar context (the guards above are schedule's
-- own code; schedule_app does not need EXECUTE — it never calls them).
GRANT USAGE ON SCHEMA schedule TO schedule_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA schedule TO schedule_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA schedule TO schedule_app;

-- game_app: the ride gameplay context.
GRANT USAGE ON SCHEMA game TO game_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA game TO game_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA game TO game_app;
-- The SchedulePort's in-tx guards (see header) need only EXECUTE,
-- granted above; USAGE on the schedule schema is what makes the guard
-- names callable. No table in schedule is readable or writable by
-- game_app.
GRANT USAGE ON SCHEMA schedule TO game_app;

-- identity_app: who logs in and what they may do.
GRANT USAGE ON SCHEMA identity TO identity_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity TO identity_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity TO identity_app;

-- The app login carries all three memberships (the server pools SET ROLE
-- per connection). The migration runner is the login in the common
-- deployment, so CURRENT_USER is the grantee.
GRANT schedule_app, game_app, identity_app TO CURRENT_USER;

-- Future tables (the Phase 08 outbox, Phase 06 balances) inherit the same
-- per-context shape without a new grant migration: whatever the runner
-- creates in a context's schema is usable by that context's role.
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA schedule
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO schedule_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA schedule
  GRANT USAGE, SELECT ON SEQUENCES TO schedule_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA game
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO game_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA game
  GRANT USAGE, SELECT ON SEQUENCES TO game_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA identity
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO identity_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA identity
  GRANT USAGE, SELECT ON SEQUENCES TO identity_app;
