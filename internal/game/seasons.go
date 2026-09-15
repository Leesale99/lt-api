package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	ValidateSeasonStatus(v, status)
}

func ValidateSeasonStatus(v *validator.Validator, status string) {
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

func (s *SeasonStore) GetAll(ctx context.Context, id int, status string, filters Filters) ([]Season, Metadata, error) {
	conds, args := []string{}, []any{}

	if id != 0 {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("id = $%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, status, version
		FROM seasons%s
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
	seasons := []Season{}

	for rows.Next() {
		var season Season
		err := rows.Scan(
			&totalRecords,
			&season.ID,
			&season.CreatedAt,
			&season.Status,
			&season.Version,
		)
		if err != nil {
			return nil, Metadata{}, err
		}

		seasons = append(seasons, season)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return seasons, metadata, nil
}

func (s *SeasonStore) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return ErrRecordNotFound
	}

	query := `
		DELETE FROM seasons
		WHERE id = $1
	`

	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrRecordNotFound
	}

	return nil
}
