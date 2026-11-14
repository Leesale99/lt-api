package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

type SeasonRepository struct {
	pool *pgxpool.Pool
}

func NewSeasonRepository(pool *pgxpool.Pool) *SeasonRepository {
	return &SeasonRepository{pool: pool}
}

func (r *SeasonRepository) Get(ctx context.Context, id int) (model.Season, error) {
	if id < 1 {
		return model.Season{}, model.ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, status, version
		FROM seasons
		WHERE id = $1
	`

	var season model.Season

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&season.ID,
		&season.CreatedAt,
		&season.Status,
		&season.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Season{}, model.ErrRecordNotFound
		default:
			return model.Season{}, err
		}
	}

	return season, nil
}

func (r *SeasonRepository) Insert(ctx context.Context, season model.Season) (model.Season, error) {
	query := `
		INSERT INTO seasons (status)
		VALUES ($1)
		RETURNING id, created_at, version
	`
	err := r.pool.QueryRow(ctx, query, season.Status).Scan(&season.ID, &season.CreatedAt, &season.Version)

	return season, err
}

func (r *SeasonRepository) Update(ctx context.Context, season model.Season) (model.Season, error) {
	query := `
		UPDATE seasons
		SET status = $1, version = version + 1
		WHERE id = $2 AND version = $3
		RETURNING version
	`
	err := r.pool.QueryRow(ctx, query, season.Status, season.ID, season.Version).Scan(&season.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Season{}, model.ErrEditConflict
		case persistence.IsTriggerViolation(err):
			// seasons_freeze_gate: a match has started and the update tried
			// to move the season back to created/open (ADR-008).
			return model.Season{}, model.ErrRecordInUse
		}
		return model.Season{}, err
	}

	return season, err
}

func (r *SeasonRepository) GetAll(ctx context.Context, id int, status model.SeasonStatus, filters persistence.Filters) ([]model.Season, persistence.Metadata, error) {
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
	`, where, filters.SortColumn(), filters.SortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.Limit(), filters.Offset())

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, persistence.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	seasons := []model.Season{}

	for rows.Next() {
		var season model.Season
		err := rows.Scan(
			&totalRecords,
			&season.ID,
			&season.CreatedAt,
			&season.Status,
			&season.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		seasons = append(seasons, season)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return seasons, metadata, nil
}

func (r *SeasonRepository) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return model.ErrRecordNotFound
	}

	query := `
		DELETE FROM seasons
		WHERE id = $1
	`

	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		// P0001 comes from the seasons delete gate (ADR-007 + ADR-008):
		// in_progress/closed seasons and open seasons with match history are
		// durable, the DB is authoritative.
		if persistence.IsTriggerViolation(err) {
			return model.ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return model.ErrRecordNotFound
	}

	return nil
}
