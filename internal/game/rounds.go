package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	v.Check(validator.PermittedValue(round.Status, roundsStatuses...), "status", "must be one of: created, open, closed")
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

	return round, err
}
