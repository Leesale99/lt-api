package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/validator"
)

// Player is a game entrant scoped to a season — who plays, not who logs in
// (identity) and not a real-world basketball athlete (future Athlete entity).
type Player struct {
	ID             int       `json:"id"`
	CreatedAt      time.Time `json:"-"`
	Name           string    `json:"name"`
	SeasonID       int       `json:"season_id"`
	FavoriteTeamID int       `json:"favorite_team_id"`
	Version        int       `json:"version"`
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

func (s *PlayerStore) GetAll(ctx context.Context, name string, favoriteTeamID int, filters Filters) ([]Player, Metadata, error) {
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
		SELECT count(*) OVER(), id, created_at, name, season_id, favorite_team_id, version
		FROM players%s
		ORDER BY %s %s, id ASC
		LIMIT $%d OFFSET $%d
	`, where, filters.sortColumn(), filters.sortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.limit(), filters.offset())

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, Metadata{}, err
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
			&player.SeasonID,
			&player.FavoriteTeamID,
			&player.Version,
		)
		if err != nil {
			return nil, Metadata{}, err
		}

		players = append(players, player)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return players, metadata, nil
}

func (s *PlayerStore) Get(ctx context.Context, id int) (Player, error) {
	if id < 1 {
		return Player{}, ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, name, season_id, favorite_team_id, version
		FROM players
		WHERE id = $1
	`
	var player Player

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&player.ID,
		&player.CreatedAt,
		&player.Name,
		&player.SeasonID,
		&player.FavoriteTeamID,
		&player.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Player{}, ErrRecordNotFound
		default:
			return Player{}, err
		}
	}

	return player, nil
}

func (s *PlayerStore) Insert(ctx context.Context, player Player) (Player, error) {
	query := `
		INSERT INTO players (name, season_id, favorite_team_id)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, version
	`
	args := []any{player.Name, player.SeasonID, player.FavoriteTeamID}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&player.ID, &player.CreatedAt, &player.Version)
	if err != nil {
		switch {
		case fkViolation(err):
			// The referenced team (or season) was deleted between the
			// handler's existence check and this write.
			return Player{}, ErrRecordNotFound
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
		RETURNING version
	`

	args := []any{player.Name, player.SeasonID, player.FavoriteTeamID, player.ID, player.Version}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&player.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Player{}, ErrEditConflict
		case fkViolation(err):
			// Same race as in Insert: the referenced team vanished
			// between validation and write.
			return Player{}, ErrRecordNotFound
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
		return ErrRecordNotFound
	}

	return nil
}
