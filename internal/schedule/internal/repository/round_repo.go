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

type RoundRepository struct {
	pool *pgxpool.Pool
}

func NewRoundRepository(pool *pgxpool.Pool) *RoundRepository {
	return &RoundRepository{pool: pool}
}

func (r *RoundRepository) Get(ctx context.Context, id int) (model.Round, error) {
	if id < 1 {
		return model.Round{}, model.ErrRecordNotFound
	}

	query := `
		SELECT id, created_at, season_id, number, status, version
		FROM rounds
		WHERE id = $1
`
	var round model.Round

	err := r.pool.QueryRow(ctx, query, id).Scan(
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
			return model.Round{}, model.ErrRecordNotFound
		default:
			return model.Round{}, err
		}
	}
	return round, nil
}

func (r *RoundRepository) Insert(ctx context.Context, round model.Round) (model.Round, error) {
	query := `
		INSERT INTO rounds (season_id, number, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, version
	`
	args := []any{round.SeasonID, round.Number, round.Status}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&round.ID, &round.CreatedAt, &round.Version)
	if err != nil {
		switch {
		case persistence.IsUniqueViolation(err):
			return model.Round{}, model.ErrDuplicateRecord
		}
		return model.Round{}, err
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
func (r *RoundRepository) updateRound(ctx context.Context, q persistence.Querier, round model.Round) (model.Round, error) {
	err := q.QueryRow(ctx, roundUpdateSQL, round.Number, round.Status, round.ID, round.Version).Scan(&round.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Round{}, model.ErrEditConflict
		case persistence.IsUniqueViolation(err):
			return model.Round{}, model.ErrDuplicateRecord
		case persistence.IsTriggerViolation(err):
			// rounds_freeze_gate: a match of this round has started and the
			// update tried to move it back to created/open (ADR-008).
			return model.Round{}, model.ErrRecordInUse
		}
		return model.Round{}, err
	}

	return round, nil
}

func (r *RoundRepository) Update(ctx context.Context, round model.Round) (model.Round, error) {
	// season_id is deliberately absent from the SET clause: it is immutable
	// after creation. matches carries a composite FK (season_id, round_id) →
	// rounds (season_id, id), so re-parenting a round would break referential
	// integrity for every match in it.
	return r.updateRound(ctx, r.pool, round)
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
func (r *RoundRepository) Open(ctx context.Context, round model.Round) (model.Round, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Round{}, err
	}
	// Commit below makes the deferred Rollback a harmless no-op (pgx returns
	// ErrTxClosed, which we discard).
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, seasonOpenToInProgressSQL, round.SeasonID); err != nil {
		return model.Round{}, err
	}

	round, err = r.updateRound(ctx, tx, round)
	if err != nil {
		return model.Round{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Round{}, err
	}

	return round, nil
}

// seasonInProgressToClosedSQL closes the season. Conditional on
// status = 'in_progress', so aiming it at a season that is not live updates
// nothing — the same no-op shape as Open's season flip.
const seasonInProgressToClosedSQL = `
		UPDATE seasons
		SET status = 'closed', version = version + 1
		WHERE id = $1 AND status = 'in_progress'
	`

// Close transitions a round open → closed and — when the round is the
// season's last — closes the season. It deliberately does NOT publish:
// RoundCompleted is the service's post-commit act (service.RoundService.Close),
// which reads the closed round's results and delivers the fat payload after
// this transaction has committed. Keeping the publish out of the repository
// is what lets the service own the transaction-to-event ordering.
//
// Since the split the close is EVENTUAL about rides (documented decision):
// the undecided won_pending rides of the round are NOT resolved here.
// Schedule closes on feed completion; the game context settles the round's
// rides (auto-unlock rounds 1-37, auto-burn round 38) inside its own
// transaction, from the event payload. The old rides backstop of
// rounds_close_gate was relaxed accordingly (migration 000014): undecided
// rides in a closed round are the designed intermediate state, not a
// corruption signal. The gap window (close committed, settlement not yet
// run) is the accepted crash gap — the settlement handler is
// idempotent, and the Phase 07 sweep / Phase 08 outbox close it.
//
// Statement order is still load-bearing: the season flip runs after the
// round flip (the mirror of Open, which flips the season before the
// round) — the season must still be live while its round closes, and
// rounds_progress_gate would refuse a round closing under an already-closed
// season. Callers must have verified the transition is open → closed; the
// version check inside updateRound still guards against a concurrent
// change.
func (r *RoundRepository) Close(ctx context.Context, round model.Round) (model.Round, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Round{}, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	// The status flip: updateRound carries the version check against
	// concurrent writers and is where the rounds gates land
	// (P0001 → ErrRecordInUse). The season is still live here.
	round, err = r.updateRound(ctx, tx, round)
	if err != nil {
		return model.Round{}, err
	}

	// The only season rule is a last-round predicate: the app-side check
	// skips the statement entirely for rounds 1-37, and the conditional
	// WHERE makes it a no-op if the season is not live.
	if round.Number == model.RoundsPerSeason {
		if _, err := tx.Exec(ctx, seasonInProgressToClosedSQL, round.SeasonID); err != nil {
			return model.Round{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Round{}, err
	}

	return round, nil
}

func (r *RoundRepository) GetAll(ctx context.Context, seasonID int, status model.RoundStatus, filters persistence.Filters) ([]model.Round, persistence.Metadata, error) {
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

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, persistence.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	rounds := []model.Round{}

	for rows.Next() {
		var round model.Round
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
			return nil, persistence.Metadata{}, err
		}

		rounds = append(rounds, round)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return rounds, metadata, nil
}

func (r *RoundRepository) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM rounds
		WHERE id = $1 AND season_id = $2
	`

	result, err := r.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		// P0001 comes from the rounds delete gate (ADR-007 one level down):
		// closed rounds are durable history, the DB is authoritative.
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
