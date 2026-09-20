CREATE TABLE IF NOT EXISTS roles (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  name text NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS permissions (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  code text NOT NULL
);

CREATE TABLE IF NOT EXISTS roles_permissions (
  role_id bigint NOT NULL
    CONSTRAINT roles_permissions_role_id_fkey REFERENCES roles ON DELETE CASCADE,
  permission_id bigint NOT NULL
    CONSTRAINT roles_permissions_permission_id_fkey REFERENCES permissions ON DELETE CASCADE,
  PRIMARY KEY (role_id, permission_id)
);

INSERT INTO roles (name)
VALUES
  ('admin'),
  ('user');

ALTER TABLE users
  ADD COLUMN role_id bigint
    CONSTRAINT users_role_id_fkey REFERENCES roles(id);

UPDATE users
SET role_id = (SELECT id FROM roles WHERE name = 'user');

ALTER TABLE users
  ALTER COLUMN role_id SET NOT NULL;

ALTER TABLE players
  ADD COLUMN user_id bigint
    CONSTRAINT players_user_id_fkey REFERENCES users(id) ON DELETE SET NULL,
  ADD CONSTRAINT players_season_id_user_id_key UNIQUE (season_id, user_id);

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

-- admin: everything
INSERT INTO roles_permissions (role_id, permission_id)
SELECT roles.id, permissions.id
FROM roles
INNER JOIN permissions ON permissions.code = ANY(ARRAY[
  'seasons:read',
  'seasons:write',
  'rounds:read',
  'rounds:write',
  'matches:read',
  'matches:write',
  'players:read',
  'players:write',
  'teams:read',
  'teams:write'
])
WHERE roles.name = 'admin';

-- user: read-only + manage own player
INSERT INTO roles_permissions (role_id, permission_id)
SELECT roles.id, permissions.id
FROM roles
INNER JOIN permissions ON permissions.code = ANY(ARRAY[
  'seasons:read',
  'rounds:read',
  'matches:read',
  'players:read',
  'players:write',
  'teams:read'
])
WHERE roles.name = 'user';
