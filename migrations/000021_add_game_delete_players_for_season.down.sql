-- Reverse of 000021: the grants go first (000018's down order), then the
-- function itself. Nothing else depends on it — the schedule service reaches
-- it through one SELECT, so the code and the schema roll back together.
REVOKE EXECUTE ON FUNCTION game.delete_players_for_season(bigint) FROM schedule_app;
REVOKE USAGE ON SCHEMA game FROM schedule_app;

DROP FUNCTION IF EXISTS game.delete_players_for_season(bigint);
