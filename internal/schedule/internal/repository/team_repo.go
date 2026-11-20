package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

type TeamRepository struct {
	pool *pgxpool.Pool
}

func NewTeamRepository(pool *pgxpool.Pool) *TeamRepository {
	return &TeamRepository{pool: pool}
}

func (r *TeamRepository) GetAll(ctx context.Context, name string, filters persistence.Filters) ([]model.Team, persistence.Metadata, error) {
	// The name filter is built only when non-empty. The catch-all alternative
	// `WHERE (name ILIKE $1 OR $1 = '')` defeats the trgm GIN index: once the
	// statement is cached, the planner can't prune the OR branch, so the query
	// degrades to a seq scan (see ADR-006).
	where := ""
	args := []any{}
	if name != "" {
		where = " WHERE name ILIKE $1"
		args = append(args, "%"+name+"%")
	}

	// fmt.Sprintf is safe here only because SortColumn()/SortDirection() are
	// validated against the caller's SortSafelist — do not interpolate any
	// other user input into this query. LIMIT/OFFSET positions trail the
	// optional name filter, hence the len(args) arithmetic.
	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, name, logo, description, version
		FROM teams%s
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
	teams := []model.Team{}

	for rows.Next() {
		var team model.Team
		err := rows.Scan(
			&totalRecords,
			&team.ID,
			&team.CreatedAt,
			&team.Name,
			&team.Logo,
			&team.Description,
			&team.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		teams = append(teams, team)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return teams, metadata, nil
}

func (r *TeamRepository) Get(ctx context.Context, id int) (model.Team, error) {
	if id < 1 {
		return model.Team{}, model.ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, name, logo, description, version
		FROM teams
		WHERE id = $1
	`

	var team model.Team

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&team.ID,
		&team.CreatedAt,
		&team.Name,
		&team.Logo,
		&team.Description,
		&team.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Team{}, model.ErrRecordNotFound
		default:
			return model.Team{}, err
		}
	}

	return team, nil
}

func (r *TeamRepository) Insert(ctx context.Context, team model.Team) (model.Team, error) {
	query := `
			INSERT INTO teams (name, logo, description)
			VALUES ($1, $2, $3)
			RETURNING id, created_at, version
	`
	args := []any{team.Name, team.Logo, team.Description}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&team.ID, &team.CreatedAt, &team.Version)

	return team, err
}

// rule:R-SCHEDULE-028
func (r *TeamRepository) Update(ctx context.Context, team model.Team) (model.Team, error) {
	query := `
		UPDATE teams
		SET name = $1, logo = $2, description = $3, version = version + 1
		WHERE id = $4 AND version = $5
		RETURNING version
	`
	args := []any{
		team.Name,
		team.Logo,
		team.Description,
		team.ID,
		team.Version,
	}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&team.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Team{}, model.ErrEditConflict
		default:
			return model.Team{}, err
		}
	}

	return team, err
}

// rule:R-SCHEDULE-025
func (r *TeamRepository) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return model.ErrRecordNotFound
	}

	query := `
		DELETE FROM teams
		WHERE id = $1
	`
	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		// teams is referenced with ON DELETE RESTRICT by matches and players:
		// a referenced team must not be deleted (domain decision). See
		// IsDeleteRestricted for the SQLSTATE codes.
		if persistence.IsDeleteRestricted(err) {
			return model.ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return model.ErrRecordNotFound
	}

	return nil
}
