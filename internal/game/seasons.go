package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/store"
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

// seasonStatusRank orders the season lifecycle for transition checks.
var seasonStatusRank = map[string]int{
	"created":     0,
	"open":        1,
	"in_progress": 2,
	"closed":      3,
}

// ValidateSeasonUpdate validates a season update (new) against the stored
// version (old): vocabulary plus no-regression. The freeze half — no
// created/open once a match has started — is the seasons_freeze_gate
// trigger (ADR-008); this check is advisory UX.
func ValidateSeasonUpdate(v *validator.Validator, old, new Season) {
	ValidateSeason(v, new)
	checkStatusRegression(v, "season", old.Status, new.Status, seasonStatusRank)
}

type SeasonStore struct {
	pool *pgxpool.Pool
}

func (s *SeasonStore) Get(ctx context.Context, id int) (Season, error) {
	if id < 1 {
		return Season{}, store.ErrRecordNotFound
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
			return Season{}, store.ErrRecordNotFound
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
			return Season{}, store.ErrEditConflict
		case store.IsTriggerViolation(err):
			// seasons_freeze_gate: a match has started and the update tried
			// to move the season back to created/open (ADR-008).
			return Season{}, store.ErrRecordInUse
		}
		return Season{}, err
	}

	return season, err
}

func (s *SeasonStore) GetAll(ctx context.Context, id int, status string, filters store.Filters) ([]Season, store.Metadata, error) {
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

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, store.Metadata{}, err
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
			return nil, store.Metadata{}, err
		}

		seasons = append(seasons, season)
	}

	if err = rows.Err(); err != nil {
		return nil, store.Metadata{}, err
	}

	metadata := store.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return seasons, metadata, nil
}

func (s *SeasonStore) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return store.ErrRecordNotFound
	}

	query := `
		DELETE FROM seasons
		WHERE id = $1
	`

	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		// P0001 comes from the seasons delete gate (ADR-007 + ADR-008):
		// in_progress/closed seasons and open seasons with match history are
		// durable, the DB is authoritative.
		if store.IsTriggerViolation(err) {
			return store.ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return store.ErrRecordNotFound
	}

	return nil
}
