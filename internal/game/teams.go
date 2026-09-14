package game

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/validator"
)

type Team struct {
	ID          int       `json:"id"`
	CreatedAt   time.Time `json:"-"`
	Name        string    `json:"name"`
	Logo        string    `json:"logo"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}

func ValidateTeam(v *validator.Validator, team Team) {
	v.Check(team.Name != "", "name", "must be provided")
	v.Check(len(team.Name) <= 500, "name", "must not be more than 500 bytes long")
	v.Check(team.Logo != "", "logo", "must be provided")
	if team.Logo != "" {
		v.Check(validImageURL(team.Logo), "logo", "must be a valid image URL ending in png, jpg, jpeg, svg or webp")
	}
	v.Check(team.Description != "", "description", "must be provided")
	v.Check(len(team.Description) <= 5000, "description", "must not be more than 5000 bytes long")
}

func validImageURL(s string) bool {
	u, err := url.ParseRequestURI(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}

	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.EscapedPath()), "."))
	return validator.PermittedValue(ext, "png", "jpg", "jpeg", "svg", "webp")
}

type TeamStore struct {
	pool *pgxpool.Pool
}

func (s *TeamStore) GetAll(ctx context.Context, name string, filters Filters) ([]Team, Metadata, error) {
	// The name filter is included only when non-empty: a bare ILIKE keeps the
	// trgm GIN index usable under generic (cached) plans, whereas the
	// alternative `WHERE (name ILIKE $1 OR $1 = '')` degenerates to a seq scan
	// once the statement is cached and $1 becomes opaque to the planner
	// (verified via EXPLAIN ANALYZE under plan_cache_mode = force_generic_plan).
	where := ""
	args := []any{}
	if name != "" {
		where = " WHERE name ILIKE $1"
		args = append(args, "%"+name+"%")
	}

	// fmt.Sprintf is safe here only because sortColumn()/sortDirection() are
	// validated against the caller's SortSafelist — do not interpolate any
	// other user input into this query. LIMIT/OFFSET positions trail the
	// optional name filter, hence the len(args) arithmetic.
	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, name, logo, description, version
		FROM teams%s
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
	teams := []Team{}

	for rows.Next() {
		var team Team
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
			return nil, Metadata{}, err
		}

		teams = append(teams, team)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return teams, metadata, nil
}

func (s *TeamStore) Get(ctx context.Context, id int) (Team, error) {
	if id < 1 {
		return Team{}, ErrRecordNotFound
	}

	query := `
			SELECT id, created_at, name, logo, description, version
			FROM teams
			WHERE id = $1
	`

	var team Team

	err := s.pool.QueryRow(ctx, query, id).Scan(
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
			return Team{}, ErrRecordNotFound
		default:
			return Team{}, err
		}
	}

	return team, nil
}

func (s *TeamStore) Insert(ctx context.Context, team Team) (Team, error) {
	query := `
			INSERT INTO teams (name, logo, description)
			VALUES ($1, $2, $3)
			RETURNING id, created_at, version
	`
	args := []any{team.Name, team.Logo, team.Description}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&team.ID, &team.CreatedAt, &team.Version)

	return team, err
}

func (s *TeamStore) Update(ctx context.Context, team Team) (Team, error) {
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

	err := s.pool.QueryRow(ctx, query, args...).Scan(&team.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Team{}, ErrEditConflict
		default:
			return Team{}, err
		}
	}

	return team, err
}

func (s *TeamStore) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return ErrRecordNotFound
	}

	query := `
		DELETE FROM teams
		WHERE id = $1
	`
	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		// teams is referenced with ON DELETE RESTRICT by matches and players:
		// a referenced team must not be deleted (domain decision), so map the
		// FK violation to a sentinel instead of leaking it. Postgres raises
		// 23001 (restrict_violation) for RESTRICT FKs and 23503
		// (foreign_key_violation) for NO ACTION; pgerrcode inlined to avoid a
		// dependency.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23001" || pgErr.Code == "23503") {
			return ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrRecordNotFound
	}

	return nil
}
