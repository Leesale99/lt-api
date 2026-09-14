CREATE TABLE teams (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  name text NOT NULL
    CONSTRAINT teams_name_check CHECK (name <> '' AND octet_length(name) <= 500),
  logo text NOT NULL
    CONSTRAINT teams_logo_check CHECK (logo <> ''),
  description text NOT NULL
    CONSTRAINT teams_description_check CHECK (description <> '' AND octet_length(description) <= 5000),
  version integer NOT NULL DEFAULT 1
);

CREATE TABLE seasons (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  status text NOT NULL DEFAULT 'created'
    CONSTRAINT seasons_status_check CHECK (status IN ('created', 'open', 'in_progress', 'closed')),
  version integer NOT NULL DEFAULT 1
);

CREATE TABLE rounds (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  season_id bigint NOT NULL
    CONSTRAINT rounds_season_id_fkey REFERENCES seasons ON DELETE CASCADE,
  number integer NOT NULL
    CONSTRAINT rounds_number_check CHECK (number BETWEEN 1 AND 38),
  status text NOT NULL DEFAULT 'created'
    CONSTRAINT rounds_status_check CHECK (status IN ('created', 'open', 'closed')),
  version integer NOT NULL DEFAULT 1,

  -- multi-column constraints
  CONSTRAINT rounds_season_id_id_key UNIQUE (season_id, id),
  CONSTRAINT rounds_season_id_number_key UNIQUE (season_id, number)
);

CREATE TABLE matches (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  season_id bigint NOT NULL
    CONSTRAINT matches_season_id_fkey REFERENCES seasons ON DELETE CASCADE,
  round_id bigint NOT NULL
    CONSTRAINT matches_round_id_fkey REFERENCES rounds ON DELETE CASCADE,
  home_team_id bigint NOT NULL
    CONSTRAINT matches_home_team_id_fkey REFERENCES teams ON DELETE RESTRICT, -- we don't want to delete matches history if we delete a team
  away_team_id bigint NOT NULL
    CONSTRAINT matches_away_team_id_fkey REFERENCES teams ON DELETE RESTRICT,
  home_odds numeric(6, 3) NOT NULL
    CONSTRAINT matches_home_odds_check CHECK (home_odds > 1),
  away_odds numeric(6, 3) NOT NULL
    CONSTRAINT matches_away_odds_check CHECK (away_odds > 1),
  home_score smallint
    CONSTRAINT matches_home_score_check CHECK (home_score IS NULL OR home_score >= 0),
  away_score smallint
    CONSTRAINT matches_away_score_check CHECK (away_score IS NULL OR away_score >= 0),
  status text NOT NULL DEFAULT 'created'
    CONSTRAINT matches_status_check CHECK (status IN ('created', 'open', 'in_progress', 'postponed', 'closed')),
  version integer NOT NULL DEFAULT 1,

  -- multi-column constraints
  CONSTRAINT matches_season_id_round_id_fkey FOREIGN KEY (season_id, round_id) REFERENCES rounds (season_id, id) ON DELETE CASCADE,
  CONSTRAINT matches_teams_differ_check CHECK (home_team_id <> away_team_id),
 CONSTRAINT matches_status_score_check CHECK (
    (status IN ('in_progress', 'closed') AND home_score IS NOT NULL)
    OR (status IN ('created', 'open', 'postponed') AND home_score IS NULL)
  ),
  CONSTRAINT matches_score_complete_check CHECK ((home_score IS NULL) = (away_score IS NULL))
);

CREATE TABLE players (
  id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  created_at timestamp(0) with time zone NOT NULL DEFAULT now(),
  season_id bigint NOT NULL
    CONSTRAINT players_season_id_fkey REFERENCES seasons ON DELETE CASCADE,
  favorite_team_id bigint NOT NULL
    CONSTRAINT players_favorite_team_id_fkey REFERENCES teams ON DELETE RESTRICT,
  name text NOT NULL 
    CONSTRAINT players_name_check CHECK (name <> '' AND octet_length(name) <= 200),
  version integer NOT NULL DEFAULT 1
);

CREATE INDEX players_season_id_idx ON players (season_id);
CREATE INDEX players_favorite_team_id_idx ON players (favorite_team_id);
CREATE INDEX rounds_season_id_idx ON rounds (season_id);
CREATE INDEX matches_round_id_idx ON matches (round_id);
CREATE INDEX matches_home_team_id_idx ON matches (home_team_id);
CREATE INDEX matches_away_team_id_idx ON matches (away_team_id);
