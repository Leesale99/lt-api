package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

// Player is a game entrant scoped to a season — who plays, not who logs in
// (identity) and not a real-world basketball athlete (future Athlete entity).
type Player struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"-"`
	Name      string    `json:"name"`
	// Nullable: since 000016 there is no players_user_id FK (identity is
	// another context), so a deleted user no longer nulls the
	// column in the DB; the column stays nullable for the historical
	// "no owner" shape.
	UserID         *int `json:"-"`
	SeasonID       int  `json:"season_id"`
	FavoriteTeamID int  `json:"favorite_team_id"`
	Version        int  `json:"version"`
}

func ValidatePlayer(v *validator.Validator, player Player) {
	v.Check(player.SeasonID > 0, "season_id", "must be provided")
	v.Check(player.Name != "", "name", "must be provided")
	v.Check(len(player.Name) <= 200, "name", "must not be more than 200 bytes long")
	v.Check(player.FavoriteTeamID > 0, "favorite_team_id", "must be provided")
}

type PlayerStore struct {
	pool *pgxpool.Pool
}

func (s *PlayerStore) GetAll(ctx context.Context, name string, favoriteTeamID int, filters persistence.Filters) ([]Player, persistence.Metadata, error) {
	// Each filter is appended as a separate predicate (AND-composed) so the
	// planner can still use the per-column indexes — do not switch to a
	// catch-all like `WHERE (name ILIKE $1 OR $1 = '')`, which defeats the
	// trgm GIN index once the statement is cached (see ADR-006).
	conds, args := []string{}, []any{}

	if name != "" {
		args = append(args, "%"+name+"%")
		conds = append(conds, fmt.Sprintf("name ILIKE $%d", len(args)))
	}
	// favorite_team_id is never 0 in the schema (FK), so 0 doubles as the
	// "no filter" sentinel from the query-string default.
	if favoriteTeamID != 0 {
		args = append(args, favoriteTeamID)
		conds = append(conds, fmt.Sprintf("favorite_team_id = $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, name, user_id, season_id, favorite_team_id, version
		FROM players%s
		ORDER BY %s %s, id ASC
		LIMIT $%d OFFSET $%d
	`, where, filters.SortColumn(), filters.SortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.Limit(), filters.Offset())

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, persistence.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	players := []Player{}

	for rows.Next() {
		player := Player{}

		err := rows.Scan(
			&totalRecords,
			&player.ID,
			&player.CreatedAt,
			&player.Name,
			&player.UserID,
			&player.SeasonID,
			&player.FavoriteTeamID,
			&player.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		players = append(players, player)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return players, metadata, nil
}

func (s *PlayerStore) Get(ctx context.Context, id int) (Player, error) {
	if id < 1 {
		return Player{}, persistence.ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, name, user_id, season_id, favorite_team_id, version
		FROM players
		WHERE id = $1
	`
	var player Player

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&player.ID,
		&player.CreatedAt,
		&player.Name,
		&player.UserID,
		&player.SeasonID,
		&player.FavoriteTeamID,
		&player.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Player{}, persistence.ErrRecordNotFound
		default:
			return Player{}, err
		}
	}

	return player, nil
}

func (s *PlayerStore) Insert(ctx context.Context, player Player) (Player, error) {
	query := `
		INSERT INTO players (name, user_id, season_id, favorite_team_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, version
	`
	args := []any{player.Name, player.UserID, player.SeasonID, player.FavoriteTeamID}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&player.ID, &player.CreatedAt, &player.Version)
	if err != nil {
		switch {
		case persistence.IsUniqueViolation(err, "players_season_id_user_id_key"):
			// A second registration of the same user in the same season.
			return Player{}, persistence.ErrDuplicateRecord
		case persistence.IsFKViolation(err):
			// Since 000016 there is no cross-context FK left on players
			// (000016) — the only remaining FK is none, so this branch is
			// currently unreachable; it stays as the generic race mapping
			// shape for when a same-context FK reappears.
			return Player{}, persistence.ErrRecordNotFound
		default:
			return Player{}, err
		}
	}

	return player, nil
}

func (s *PlayerStore) Update(ctx context.Context, player Player) (Player, error) {
	query := `
		UPDATE players
		SET name = $1, season_id = $2, favorite_team_id = $3, version = version + 1
		WHERE id = $4 AND version = $5
		RETURNING user_id, version
	`

	args := []any{player.Name, player.SeasonID, player.FavoriteTeamID, player.ID, player.Version}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&player.UserID, &player.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Player{}, persistence.ErrEditConflict
		case persistence.IsFKViolation(err):
			// Since 000016 no FK is left on players; the team
			// existence check is the api handler's, before this write.
			return Player{}, persistence.ErrRecordNotFound
		default:
			return Player{}, err
		}
	}

	return player, nil
}

func (s *PlayerStore) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM players
		WHERE id = $1 AND season_id = $2
	`
	result, err := s.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return persistence.ErrRecordNotFound
	}

	return nil
}

// ExistsForTeam reports whether any player names the team as their
// favorite — the game half of the api team-delete refusal (000016: the
// players_favorite_team_id_fkey RESTRICT is gone with the cross-context
// FKs, so the "team still referenced" check is an api-level composition
// of schedule's and game's facts; same ErrRecordInUse → 409 surface).
func (s *PlayerStore) ExistsForTeam(ctx context.Context, teamID int) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM players WHERE favorite_team_id = $1)`, teamID).Scan(&exists)
	return exists, err
}

// DeleteForSeason deletes every player registered in the season — the
// game half of the api season-delete cascade (000016: the FK CASCADE is
// gone with the cross-context FKs, so the api handler deletes the
// season's players through game before deleting the season; the row
// outcome is the old cascade's). A ride still referencing a player trips
// rides_player_id_fkey — a same-context FK — so a season that owns rides
// is refused: ErrRecordInUse, the 409 the old cascade produced.
func (s *PlayerStore) DeleteForSeason(ctx context.Context, seasonID int) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM players WHERE season_id = $1`, seasonID)
	if err != nil {
		if persistence.IsDeleteRestricted(err) {
			return persistence.ErrRecordInUse
		}
		return err
	}
	_ = result
	return nil
}
