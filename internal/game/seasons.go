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

// SeasonStatus is the lifecycle state of a season.
type SeasonStatus string

const (
	SeasonCreated    SeasonStatus = "created"
	SeasonOpen       SeasonStatus = "open"
	SeasonInProgress SeasonStatus = "in_progress"
	SeasonClosed     SeasonStatus = "closed"
)

type Season struct {
	ID        int          `json:"id"`
	CreatedAt time.Time    `json:"-"`
	Status    SeasonStatus `json:"status"`
	Version   int          `json:"version"`
}

var seasonStatuses = []SeasonStatus{SeasonCreated, SeasonOpen, SeasonInProgress, SeasonClosed}

func ValidateSeason(v *validator.Validator, season Season) {
	v.Check(season.Status != "", "status", "must be provided")
	ValidateSeasonStatus(v, season.Status)
}

func ValidateSeasonStatus(v *validator.Validator, status SeasonStatus) {
	v.Check(validator.PermittedValue(status, seasonStatuses...), "status", "must be one of: created, open, in_progress, closed")
}

// seasonStatusRank orders the season lifecycle for transition checks.
var seasonStatusRank = map[SeasonStatus]int{
	SeasonCreated:    0,
	SeasonOpen:       1,
	SeasonInProgress: 2,
	SeasonClosed:     3,
}

// ValidateNewSeason validates a season about to be created. A new season
// always starts in created: the client cannot choose the status, and the
// lifecycle progression belongs to Update (see checkStatusRegression).
func ValidateNewSeason(v *validator.Validator, season Season) {
	ValidateSeason(v, season)
	v.Check(season.Status == SeasonCreated, "status", "new seasons can only have status created")
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

func (s *SeasonStore) GetAll(ctx context.Context, id int, status SeasonStatus, filters store.Filters) ([]Season, store.Metadata, error) {
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
