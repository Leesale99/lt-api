package game

import (
	"context"
	"errors"
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

func (s *PlayerStore) Delete(ctx context.Context, id, seasonId int) error {
	query := `
		DELETE FROM players
		WHERE id = $1 AND season_id = $2
	`
	result, err := s.pool.Exec(ctx, query, id, seasonId)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrRecordNotFound
	}

	return nil
}
