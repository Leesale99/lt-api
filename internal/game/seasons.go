package game

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/validator"
)

type Season struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"-"`
	Status    string    `json:"status"`
	Version   int       `json:"version"`
}

var seasonStatuses = []string{"created", "open", "in_progress", "closed"}

func ValidateSeason(v *validator.Validator, season Season) {
	status := strings.ToLower(season.Status)

	v.Check(status != "", "status", "must be provided")
	v.Check(validator.PermittedValue(status, seasonStatuses...), "status", "Must be one of: created, open, in_progress, closed")
}

type SeasonStore struct {
	pool *pgxpool.Pool
}

func (s *SeasonStore) Get(ctx context.Context, id int) (Season, error) {
	if id < 1 {
		return Season{}, ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, status, version
		FROM seasons
		WHERE id = $1
	`

	var season Season

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&season.ID,
		&season.CreatedAt,
		&season.Status,
		&season.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Season{}, ErrRecordNotFound
		default:
			return Season{}, err
		}
	}

	return season, nil
}

func (s *SeasonStore) Insert(ctx context.Context, season Season) (Season, error) {
	query := `
		INSERT INTO seasons (status)
		VALUES ($1)
		RETURNING id, created_at, version
	`
	err := s.pool.QueryRow(ctx, query, season.Status).Scan(&season.ID, &season.CreatedAt, &season.Version)

	return season, err
}

func (s *SeasonStore) Update(ctx context.Context, season Season) (Season, error) {
	query := `
		UPDATE seasons
		SET status = $1, version = version + 1
		WHERE id = $2 AND version = $3
		RETURNING version
	`
	err := s.pool.QueryRow(ctx, query, season.Status, season.ID, season.Version).Scan(&season.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Season{}, ErrEditConflict
		}
		return Season{}, err
	}

	return season, err
}
