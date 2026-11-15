-- Reverse of 000018: the memberships go first, then the per-schema
-- grants, then the roles themselves (they own no objects — only
-- privileges, which the REVOKEs drop).

REVOKE schedule_app, game_app, identity_app FROM CURRENT_USER;

-- The default-privilege entries are grants tied to the roles: drop them
-- before the roles, or DROP ROLE refuses (objects depend on it).
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA schedule REVOKE ALL ON TABLES FROM schedule_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA schedule REVOKE ALL ON SEQUENCES FROM schedule_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA game REVOKE ALL ON TABLES FROM game_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA game REVOKE ALL ON SEQUENCES FROM game_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA identity REVOKE ALL ON TABLES FROM identity_app;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA identity REVOKE ALL ON SEQUENCES FROM identity_app;

DROP FUNCTION IF EXISTS schedule.round_open_guard(bigint);
DROP FUNCTION IF EXISTS schedule.destination_open_guard(bigint);

REVOKE ALL ON ALL TABLES IN SCHEMA schedule FROM schedule_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA schedule FROM schedule_app;
REVOKE USAGE ON SCHEMA schedule FROM schedule_app;
REVOKE ALL ON ALL TABLES IN SCHEMA game FROM game_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA game FROM game_app;
REVOKE USAGE ON SCHEMA game FROM game_app;
-- Covers the read grants an intermediate state of this migration's up
-- file may have left on the two port tables (matches/rounds) — harmless
-- when nothing was granted, required when it was.
REVOKE ALL ON ALL TABLES IN SCHEMA schedule FROM game_app;
REVOKE USAGE ON SCHEMA schedule FROM game_app;
REVOKE ALL ON ALL TABLES IN SCHEMA identity FROM identity_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA identity FROM identity_app;
REVOKE USAGE ON SCHEMA identity FROM identity_app;

ALTER ROLE schedule_app RESET search_path;
ALTER ROLE game_app RESET search_path;
ALTER ROLE identity_app RESET search_path;

DROP ROLE IF EXISTS schedule_app;
DROP ROLE IF EXISTS game_app;
DROP ROLE IF EXISTS identity_app;
