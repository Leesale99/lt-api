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

// roundStatusRank orders the round lifecycle for transition checks.
var roundStatusRank = map[string]int{
	"created": 0,
	"open":    1,
	"closed":  2,
}

// ValidateRoundUpdate validates a round update (new) against the stored
// version (old): vocabulary plus no-regression. The freeze half — no
// created/open once a match of the round has started — is the
// rounds_freeze_gate trigger (ADR-008); this check is advisory UX.
func ValidateRoundUpdate(v *validator.Validator, old, new Round) {
	ValidateRound(v, new)
	checkStatusRegression(v, "round", old.Status, new.Status, roundStatusRank)
}

type RoundStore struct {
	pool *pgxpool.Pool
}

func (s *RoundStore) Get(ctx context.Context, id int) (Round, error) {
	if id < 1 {
		return Round{}, store.ErrRecordNotFound
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
			return Round{}, store.ErrRecordNotFound
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
		case store.IsUniqueViolation(err):
			return Round{}, store.ErrDuplicateRecord
		}
		return Round{}, err
	}

	return round, nil
}

// roundUpdateSQL is the round-update statement, shared by Update (plain
// write) and Open (write inside the season-flip transaction) so both paths
// map errors identically.
const roundUpdateSQL = `
		UPDATE rounds
		SET number = $1, status = $2, version = version + 1
		WHERE id = $3 AND version = $4
		RETURNING version
	`

// updateRound executes the shared round-update statement on q and maps
// store-level errors to sentinels.
func (s *RoundStore) updateRound(ctx context.Context, q store.Querier, round Round) (Round, error) {
	err := q.QueryRow(ctx, roundUpdateSQL, round.Number, round.Status, round.ID, round.Version).Scan(&round.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Round{}, store.ErrEditConflict
		case store.IsUniqueViolation(err):
			return Round{}, store.ErrDuplicateRecord
		case store.IsTriggerViolation(err):
			// rounds_freeze_gate: a match of this round has started and the
			// update tried to move it back to created/open (ADR-008).
			return Round{}, store.ErrRecordInUse
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
	return s.updateRound(ctx, s.pool, round)
}

// seasonOpenToInProgressSQL flips the round's season from open to
// in_progress (ADR-008 point 3). Conditional on status = 'open', so calling
// it repeatedly — or on a season already in_progress — is a no-op.
const seasonOpenToInProgressSQL = `
		UPDATE seasons
		SET status = 'in_progress', version = version + 1
		WHERE id = $1 AND status = 'open'
	`

// Open transitions a round created → open and, in the same transaction,
// flips its season open → in_progress: opening a round starts the
// competitive phase, and the flip rides a write that already happens
// (no worker, no stale state). Callers must have verified the transition is
// created → open; the version check inside updateRound still guards against
// a concurrent change between that check and this write.
func (s *RoundStore) Open(ctx context.Context, round Round) (Round, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Round{}, err
	}
	// Commit below makes the deferred Rollback a harmless no-op (pgx returns
	// ErrTxClosed, which we discard).
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, seasonOpenToInProgressSQL, round.SeasonID); err != nil {
		return Round{}, err
	}

	round, err = s.updateRound(ctx, tx, round)
	if err != nil {
		return Round{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Round{}, err
	}

	return round, nil
}

func (s *RoundStore) GetAll(ctx context.Context, seasonID int, status string, filters store.Filters) ([]Round, store.Metadata, error) {
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
	`, where, filters.SortColumn(), filters.SortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.Limit(), filters.Offset())

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, store.Metadata{}, err
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
			return nil, store.Metadata{}, err
		}

		rounds = append(rounds, round)
	}

	if err = rows.Err(); err != nil {
		return nil, store.Metadata{}, err
	}

	metadata := store.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return rounds, metadata, nil
}

func (s *RoundStore) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM rounds
		WHERE id = $1 AND season_id = $2
	`

	result, err := s.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		// P0001 comes from the rounds delete gate (ADR-007 one level down):
		// closed rounds are durable history, the DB is authoritative.
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
