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

// roundsPerSeason is the total number of rounds in a season. The league has
// 20 teams playing each other home and away (double round-robin): 38 rounds
// of 10 matches each.
const roundsPerSeason = 38

type Round struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"-"`
	SeasonID  int       `json:"season_id"`
	Number    int       `json:"number"`
	Status    string    `json:"status"`
	Version   int       `json:"version"`
}

var roundsStatuses = []string{"created", "open", "closed"}

func ValidateRound(v *validator.Validator, round Round) {
	v.Check(round.SeasonID > 0, "season_id", "must be provided")
	v.Check(round.Number > 0 && round.Number <= roundsPerSeason, "number", fmt.Sprintf("must be between 1 and %d", roundsPerSeason))
	v.Check(round.Status != "", "status", "must be provided")
	ValidateRoundStatus(v, round.Status)
}

func ValidateRoundStatus(v *validator.Validator, status string) {
	v.Check(validator.PermittedValue(status, roundsStatuses...), "status", "must be one of: created, open, closed")
}

type RoundStore struct {
	pool *pgxpool.Pool
}

func (s *RoundStore) Get(ctx context.Context, id int) (Round, error) {
	if id < 1 {
		return Round{}, ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, season_id, number, status, version
		FROM rounds
		WHERE id = $1
`
	var round Round

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&round.ID,
		&round.CreatedAt,
		&round.SeasonID,
		&round.Number,
		&round.Status,
		&round.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Round{}, ErrRecordNotFound
		default:
			return Round{}, err
		}
	}
	return round, nil
}

func (s *RoundStore) Insert(ctx context.Context, round Round) (Round, error) {
	query := `
		INSERT INTO rounds (season_id, number, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, version
	`
	args := []any{round.SeasonID, round.Number, round.Status}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&round.ID, &round.CreatedAt, &round.Version)
	if err != nil {
		switch {
		case uniqueViolation(err):
			return Round{}, ErrDuplicateRecord
		}
		return Round{}, err
	}

	return round, nil
}

func (s *RoundStore) Update(ctx context.Context, round Round) (Round, error) {
	// season_id is deliberately absent from the SET clause: it is immutable
	// after creation. matches carries a composite FK (season_id, round_id) →
	// rounds (season_id, id), so re-parenting a round would break referential
	// integrity for every match in it.
	query := `
		UPDATE rounds
		SET number = $1, status = $2, version = version + 1
		WHERE id = $3 AND version = $4
		RETURNING version
	`
	err := s.pool.QueryRow(ctx, query, round.Number, round.Status, round.ID, round.Version).Scan(&round.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Round{}, ErrEditConflict
		case uniqueViolation(err):
			return Round{}, ErrDuplicateRecord
		}
		return Round{}, err
	}

	return round, err
}

func (s *RoundStore) GetAll(ctx context.Context, seasonID int, status string, filters Filters) ([]Round, Metadata, error) {
	conds, args := []string{}, []any{}

	if seasonID != 0 {
		args = append(args, seasonID)
		conds = append(conds, fmt.Sprintf("season_id = $%d", len(args)))
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
		SELECT count(*) OVER(), id, created_at, season_id, number, status, version
		FROM rounds%s
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
	rounds := []Round{}

	for rows.Next() {
		var round Round
		err := rows.Scan(
			&totalRecords,
			&round.ID,
			&round.CreatedAt,
			&round.SeasonID,
			&round.Number,
			&round.Status,
			&round.Version,
		)
		if err != nil {
			return nil, Metadata{}, err
		}

		rounds = append(rounds, round)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return rounds, metadata, nil
}

func (s *RoundStore) Delete(ctx context.Context, id int) error {
	if id < 1 {
		return ErrRecordNotFound
	}

	query := `
		DELETE FROM rounds
		WHERE id = $1
	`

	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		// P0001 comes from the rounds_delete_gate trigger (ADR-007 one level
		// down): closed rounds are durable history, the DB is authoritative.
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
