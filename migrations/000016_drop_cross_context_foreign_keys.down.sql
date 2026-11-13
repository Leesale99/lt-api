-- Reverse of 000016: the cross-context FKs re-appear, now schema-qualified
-- (the schemas exist since 000015). Re-adding validates every existing row:
-- a table holding orphaned references (written while the FK was gone) must
-- be cleaned before this down path can run.

ALTER TABLE game.players
  ADD CONSTRAINT players_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES identity.users (id) ON DELETE SET NULL;

ALTER TABLE game.players
  ADD CONSTRAINT players_season_id_fkey
  FOREIGN KEY (season_id) REFERENCES schedule.seasons (id) ON DELETE CASCADE;

ALTER TABLE game.players
  ADD CONSTRAINT players_favorite_team_id_fkey
  FOREIGN KEY (favorite_team_id) REFERENCES schedule.teams (id) ON DELETE RESTRICT;

ALTER TABLE game.rides
  ADD CONSTRAINT rides_match_id_fkey
  FOREIGN KEY (match_id) REFERENCES schedule.matches (id) ON DELETE RESTRICT;

ALTER TABLE game.rides
  ADD CONSTRAINT rides_team_id_fkey
  FOREIGN KEY (team_id) REFERENCES schedule.teams (id) ON DELETE RESTRICT;
