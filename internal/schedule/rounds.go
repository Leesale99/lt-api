package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"log/slog"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

// roundsPerSeason is the total number of rounds in a season. The league has
// 20 teams playing each other home and away (double round-robin): 38 rounds
// of 10 matches each.
const roundsPerSeason = 38

type RoundStatus string

var (
	RoundCreated RoundStatus = "created"
	RoundOpen    RoundStatus = "open"
	RoundClosed  RoundStatus = "closed"
)

type Round struct {
	ID        int         `json:"id"`
	CreatedAt time.Time   `json:"-"`
	SeasonID  int         `json:"season_id"`
	Number    int         `json:"number"`
	Status    RoundStatus `json:"status"`
	Version   int         `json:"version"`
}

var roundStatuses = []RoundStatus{RoundCreated, RoundOpen, RoundClosed}

func ValidateRound(v *validator.Validator, round Round) {
	v.Check(round.SeasonID > 0, "season_id", "must be provided")
	v.Check(round.Number > 0 && round.Number <= roundsPerSeason, "number", fmt.Sprintf("must be between 1 and %d", roundsPerSeason))
	v.Check(round.Status != "", "status", "must be provided")
	ValidateRoundStatus(v, round.Status)
}

func ValidateRoundStatus(v *validator.Validator, status RoundStatus) {
	v.Check(validator.PermittedValue(status, roundStatuses...), "status", "must be one of: created, open, closed")
}

// ValidateNewRound validates a round about to be created. A new round always
// starts in created: opening it belongs to the update flow, whose
// created → open transition also starts the season (RoundStore.Open,
// ADR-008). Creating a round directly as open would skip that flip.
func ValidateNewRound(v *validator.Validator, round Round) {
	ValidateRound(v, round)
	v.Check(round.Status == RoundCreated, "status", "new rounds can only have status created")
}

// roundStatusRank orders the round lifecycle for transition checks.
var roundStatusRank = map[RoundStatus]int{
	RoundCreated: 0,
	RoundOpen:    1,
	RoundClosed:  2,
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

	// bus/log carry the process event exchange and its logger into Close's
	// post-commit publish. Both are nil-safe (a bare store publishes
	// nothing) and set by schedule.NewService — the composition point.
	bus *eventbus.Bus
	log *slog.Logger
}

func (s *RoundStore) Get(ctx context.Context, id int) (Round, error) {
	if id < 1 {
		return Round{}, persistence.ErrRecordNotFound
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
			return Round{}, persistence.ErrRecordNotFound
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
		case persistence.IsUniqueViolation(err):
			return Round{}, persistence.ErrDuplicateRecord
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
func (s *RoundStore) updateRound(ctx context.Context, q persistence.Querier, round Round) (Round, error) {
	err := q.QueryRow(ctx, roundUpdateSQL, round.Number, round.Status, round.ID, round.Version).Scan(&round.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Round{}, persistence.ErrEditConflict
		case persistence.IsUniqueViolation(err):
			return Round{}, persistence.ErrDuplicateRecord
		case persistence.IsTriggerViolation(err):
			// rounds_freeze_gate: a match of this round has started and the
			// update tried to move it back to created/open (ADR-008).
			return Round{}, persistence.ErrRecordInUse
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
	defer func() { _ = tx.Rollback(ctx) }()

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

// seasonInProgressToClosedSQL closes the season. Conditional on
// status = 'in_progress', so aiming it at a season that is not live updates
// nothing — the same no-op shape as Open's season flip.
const seasonInProgressToClosedSQL = `
		UPDATE seasons
		SET status = 'closed', version = version + 1
		WHERE id = $1 AND status = 'in_progress'
	`

// Close transitions a round open → closed and — when the round is the
// season's last — closes the season, then publishes RoundCompleted
// post-commit (ADR-027): the ride settlement is game's, driven by this
// event.
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
func (s *RoundStore) Close(ctx context.Context, round Round) (Round, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Round{}, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	// The status flip: updateRound carries the version check against
	// concurrent writers and is where the rounds gates land
	// (P0001 → ErrRecordInUse). The season is still live here.
	round, err = s.updateRound(ctx, tx, round)
	if err != nil {
		return Round{}, err
	}

	// The only season rule is a last-round predicate: the app-side check
	// skips the statement entirely for rounds 1-37, and the conditional
	// WHERE makes it a no-op if the season is not live.
	if round.Number == roundsPerSeason {
		if _, err := tx.Exec(ctx, seasonInProgressToClosedSQL, round.SeasonID); err != nil {
			return Round{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Round{}, err
	}

	// Post-commit publish (ADR-027). Fat payload: the round's full result
	// set rides in the event, so game's settlement never joins back into
	// schedule's tables.
	if s.bus != nil {
		results, err := resultsForRound(ctx, s.pool, round.ID)
		if err != nil {
			s.logCloseGap(ctx, round, "results read", err)
			return round, nil
		}
		event := RoundCompleted{
			EventID:  NewEventID(),
			RoundID:  round.ID,
			SeasonID: round.SeasonID,
			IsFinal:  round.Number == roundsPerSeason,
			Results:  results,
		}
		if err := s.bus.Publish(context.WithoutCancel(ctx), event); err != nil && s.log != nil {
			// Crash gap: the close is committed; a failed settlement is the
			// gap the Phase 07 sweep will close. The request must not
			// fail — its write is already durable.
			s.log.Warn("event handler failed after commit; awaiting the Phase 07 sweep",
				"event_type", "schedule.RoundCompleted", "round_id", round.ID, "error", err)
		}
	}

	return round, nil
}

// logCloseGap reports a dropped post-commit event at the close site.
func (s *RoundStore) logCloseGap(ctx context.Context, round Round, stage string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn("RoundCompleted event dropped after commit; awaiting the Phase 07 sweep",
		"stage", stage, "round_id", round.ID, "error", err)
}

func (s *RoundStore) GetAll(ctx context.Context, seasonID int, status RoundStatus, filters persistence.Filters) ([]Round, persistence.Metadata, error) {
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
		return nil, persistence.Metadata{}, err
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

func (s *RoundStore) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM rounds
		WHERE id = $1 AND season_id = $2
	`

	result, err := s.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		// P0001 comes from the rounds delete gate (ADR-007 one level down):
		// closed rounds are durable history, the DB is authoritative.
		if persistence.IsTriggerViolation(err) {
			return persistence.ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return persistence.ErrRecordNotFound
	}

	return nil
}
