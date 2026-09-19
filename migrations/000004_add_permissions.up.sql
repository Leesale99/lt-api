CREATE TABLE IF NOT EXISTS permissions (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  code text NOT NULL
);

CREATE TABLE IF NOT EXISTS users_permissions (
  user_id bigint NOT NULL
    CONSTRAINT users_permissions_user_id_fkey REFERENCES users ON DELETE CASCADE,
  permission_id bigint NOT NULL
    CONSTRAINT users_permissions_permission_id_fkey REFERENCES users ON DELETE CASCADE,
  PRIMARY KEY (user_id, permission_id)
);

INSERT INTO permissions (code)
VALUES 
  ( 'seasons:read' ),
  ( 'seasons:write' ),
  ( 'rounds:read' ),
  ( 'rounds:write' ),
  ( 'matches:read' ),
  ( 'matches:write' ),
  ( 'players:read' ),
  ( 'players:write' ),
  ( 'teams:read' ),
  ( 'teams:write' );
