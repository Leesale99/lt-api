// Package postgres is the game module's storage adapter: the SQL behind the
// core's repository ports, and the translation of every Postgres fact into
// the domain's values and sentinels. It is the only game package that reads
// SQLSTATEs — nothing above it sees a pgx row, a Postgres error code or a
// storage-shaped error.
package postgres

// The pre-refactor rides SQL, unchanged: since the Game/Schedule split every
// calendar fact reaches game through the Schedule port (round guards) or the
// event payloads (results), so no statement here touches matches or rounds.
// The money columns (tokens_locked, bonus_acc) are the ledger's — this file
// writes state, continuation and streak; the birth INSERT is the one
// exception (a ride is born carrying its locked tokens).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// RideRepository is the Postgres implementation of port.RideRepository; the
// assertion pins the adapter against the core's consumer-side interface, so a
// signature drift fails here rather than at the composition root.
type RideRepository struct {
	pool *pgxpool.Pool
}

var _ port.RideRepository = (*RideRepository)(nil)

func NewRideRepository(pool *pgxpool.Pool) *RideRepository {
	return &RideRepository{pool: pool}
}

// rideColumns is the projection every read uses, in the order rideFromRow
// scans it.
const rideColumns = `id, player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak, created_at, version`

// Insert admits locked only: rides are created locked (ADR-019), and since
// 000012 the rides_state_insert_gate refuses every other state — all other
// states must be reached through a gated UPDATE. The domain's ride.Create
// (the only production caller) stamps the locked state before this runs.
func (r *RideRepository) Insert(ctx context.Context, ride domain.Ride) (domain.Ride, error) {
	return insertRide(ctx, r.pool, ride)
}

// insertSQL is the ride's birth statement: one row, born locked with its
// initial money columns (tokens_locked, base_at_lock, bonus_acc) — the
// rides_tokens_locked_check admits no zero-token birth (ADR-019: no
// DEFAULT, the initial-state contract is the domain's), so the lock and the
// row are one statement. The round-open fact is the Schedule port's
// RoundOpenTx gate now: the use case pins the round row inside the command
// transaction before this INSERT runs.
const insertSQL = `
		INSERT INTO rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, version
	`

// insertRide runs the birth statement on any querier — the pool (Insert, the
// test-planter path) or the caller's transaction (InsertTx, the idempotent
// command path).
func insertRide(ctx context.Context, q persistence.Querier, ride domain.Ride) (domain.Ride, error) {
	args := []any{ride.PlayerID, ride.TeamID, ride.MatchID, ride.State, ride.TokensLocked, ride.BaseAtLock, ride.Acc, ride.Streak}

	err := q.QueryRow(ctx, insertSQL, args...).Scan(&ride.ID, &ride.CreatedAt, &ride.Version)
	if err != nil {
		switch {
		case persistence.IsFKViolation(err):
			// The referenced player, team or match does not exist — the
			// transport validates only positivity, so an unknown parent can
			// only be caught here.
			return domain.Ride{}, domain.ErrNotFound
		case persistence.IsTriggerViolation(err):
			// rides_state_insert_gate: a non-locked state tried to be born
			// (ADR-019). Same 409 surface as every other gate refusal.
			return domain.Ride{}, domain.ErrInvalidTransition
		default:
			if verr, ok := rideCheckViolation(err); ok {
				return domain.Ride{}, verr
			}
			return domain.Ride{}, err
		}
	}

	return ride, nil
}

// rideMoneyBounds maps each of the four rides money CHECKs (000006) to the
// column it bounds. The INSERT is this store's only money-carrying statement
// — every UPDATE omits tokens_locked, base_at_lock and bonus_acc, so these
// CHECKs are reachable at birth only.
var rideMoneyBounds = []struct {
	constraint string
	field      string
}{
	{"rides_tokens_locked_check", "tokens_locked"},
	{"rides_base_at_lock_check", "base_at_lock"},
	{"rides_bonus_acc_check", "bonus_acc"},
	{"rides_streak_check", "streak"},
}

// rideCheckViolation turns a 23514 from one of the rides money CHECKs into the
// module's 422: a domain.ValidationError on the column the constraint bounds,
// naming the violated check (R-GAME-004). Mapped by constraint name, so the
// rides table's other CHECKs (rides_state_check) keep falling through.
func rideCheckViolation(err error) (*domain.ValidationError, bool) {
	for _, bound := range rideMoneyBounds {
		if persistence.IsCheckViolation(err, bound.constraint) {
			return domain.NewValidationError(map[string]string{
				bound.field: "violates " + bound.constraint,
			}), true
		}
	}
	return nil, false
}

// InsertTx writes the created ride on the caller's transaction — RideCreate
// is idempotent (ADR-024), so the write shares the claim's transaction. The
// ride arrives post-Create (state, acc and streak are the domain command's),
// and the ride's round-open fact was pinned through Schedule.RoundOpenTx
// BEFORE this runs (the FOR SHARE lock is held until the command tx commits).
func (r *RideRepository) InsertTx(ctx context.Context, tx pgx.Tx, ride domain.Ride) (domain.Ride, error) {
	return insertRide(ctx, tx, ride)
}

// LockedForMatchTx reads the locked rides of one match, on the caller's
// transaction — the settlement handler's scan. The match id comes from the
// event payload; no join: rides owns the match_id column (ADR-018).
func (r *RideRepository) LockedForMatchTx(ctx context.Context, tx pgx.Tx, matchID int) ([]domain.Ride, error) {
	return scanRides(tx.Query(ctx, `
		SELECT `+rideColumns+`
		FROM rides
		WHERE match_id = $1 AND state = 'locked'
		ORDER BY id
	`, matchID))
}

// UndecidedForMatchesTx reads the undecided (won_pending) rides of the given
// matches, on the caller's transaction — the round settlement's sweep. The
// match ids come from the RoundCompleted payload; no join.
func (r *RideRepository) UndecidedForMatchesTx(ctx context.Context, tx pgx.Tx, matchIDs []int) ([]domain.Ride, error) {
	if len(matchIDs) == 0 {
		return nil, nil
	}

	return scanRides(tx.Query(ctx, `
		SELECT `+rideColumns+`
		FROM rides
		WHERE match_id = ANY($1) AND state = 'won_pending'
		ORDER BY id
	`, matchIDs))
}

// scanRides materializes a ride rows result.
func scanRides(rows pgx.Rows, err error) ([]domain.Ride, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rides := []domain.Ride{}
	for rows.Next() {
		var ride domain.Ride
		if err := rows.Scan(
			&ride.ID,
			&ride.PlayerID,
			&ride.TeamID,
			&ride.MatchID,
			&ride.State,
			&ride.TokensLocked,
			&ride.BaseAtLock,
			&ride.Acc,
			&ride.Streak,
			&ride.CreatedAt,
			&ride.Version,
		); err != nil {
			return nil, err
		}
		rides = append(rides, ride)
	}
	return rides, rows.Err()
}

func (r *RideRepository) Get(ctx context.Context, id int) (domain.Ride, error) {
	query := `
		SELECT ` + rideColumns + `
		FROM rides
		WHERE id = $1
	`
	var ride domain.Ride

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&ride.ID,
		&ride.PlayerID,
		&ride.TeamID,
		&ride.MatchID,
		&ride.State,
		&ride.TokensLocked,
		&ride.BaseAtLock,
		&ride.Acc,
		&ride.Streak,
		&ride.CreatedAt,
		&ride.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.Ride{}, domain.ErrNotFound
		default:
			return domain.Ride{}, err
		}
	}

	return ride, nil
}

func (r *RideRepository) GetAll(ctx context.Context, id, playerID, teamID, matchID int, state domain.RideState, filters persistence.Filters) ([]domain.Ride, persistence.Metadata, error) {
	conds, args := []string{}, []any{}
	if id != 0 {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("id = $%d", len(args)))
	}
	if playerID != 0 {
		args = append(args, playerID)
		conds = append(conds, fmt.Sprintf("player_id = $%d", len(args)))
	}
	if teamID != 0 {
		args = append(args, teamID)
		conds = append(conds, fmt.Sprintf("team_id = $%d", len(args)))
	}
	if matchID != 0 {
		args = append(args, matchID)
		conds = append(conds, fmt.Sprintf("match_id = $%d", len(args)))
	}
	if state != "" {
		args = append(args, state)
		conds = append(conds, fmt.Sprintf("state = $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak, version
		FROM rides%s
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
	rides := []domain.Ride{}

	for rows.Next() {
		var ride domain.Ride

		err := rows.Scan(
			&totalRecords,
			&ride.ID,
			&ride.PlayerID,
			&ride.TeamID,
			&ride.MatchID,
			&ride.State,
			&ride.TokensLocked,
			&ride.BaseAtLock,
			&ride.Acc,
			&ride.Streak,
			&ride.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		rides = append(rides, ride)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return rides, metadata, nil
}

// arrivalUpdateSQL is the settlement handlers' arrival write (post-commit,
// behind the MatchResultRecorded/RoundCompleted events): a single-ride state
// flip whose WHERE re-checks the contended facts at the write instant — the
// ride's version and its from-state — so a concurrent writer (a player
// decision that landed first, a settlement replay) turns the second write
// into a 0-row no-op. No match join: the destination is derived from the
// event payload and verified against the committed result by the use case's
// resultAgreement check before this write (the app-level enforcement home of
// the old rides_state_gate check 5 since migration 000017).
//
// Money columns are deliberately absent from the SET clause: the release of
// the accrued bonus (unlocked|lost arrivals) and the accrual (won arrivals)
// are the ledger's writes, ordered before/after this flip inside the same
// transaction (rides_money_state_coupling (000013) couples the unlocked|lost
// arrivals to zero acc, so the release must precede the flip).
const arrivalUpdateSQL = `
		UPDATE rides
		SET state = $2, version = version + 1
		WHERE id = $1 AND version = $3 AND state = $4
		RETURNING id, player_id, team_id, created_at, state, tokens_locked,
			base_at_lock, bonus_acc, streak, version
	`

// ArriveTx writes one settlement arrival (from-state → to-state) on the
// caller's transaction. 0 rows is classified after the fact
// (classifyArrivalRefusal): a ride gone mid-tx is domain.ErrNotFound, a moved
// row is domain.ErrInvalidTransition — in the single-settlement world of the
// claimed event, both are bug-shaped.
func (r *RideRepository) ArriveTx(ctx context.Context, tx pgx.Tx, rideID int, from, to domain.RideState, claimedVersion int) (domain.Ride, error) {
	var ride domain.Ride

	err := tx.QueryRow(ctx, arrivalUpdateSQL, rideID, to, claimedVersion, from).Scan(
		&ride.ID,
		&ride.PlayerID,
		&ride.TeamID,
		&ride.CreatedAt,
		&ride.State,
		&ride.TokensLocked,
		&ride.BaseAtLock,
		&ride.Acc,
		&ride.Streak,
		&ride.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.Ride{}, r.classifyArrivalRefusal(ctx, rideID, claimedVersion)
		case persistence.IsTriggerViolation(err):
			// rides_state_gate (ADR-020): the DB refused the arrival — the
			// state graph (checks 1–3); result agreement is the use case's
			// app-level check since migration 000017.
			return domain.Ride{}, domain.ErrInvalidTransition
		case persistence.IsCheckViolation(err, ridesMoneyStateCoupling):
			return domain.Ride{}, domain.ErrInvalidTransition
		default:
			return domain.Ride{}, err
		}
	}

	return ride, nil
}

// classifyArrivalRefusal names the 0-row causes of ArriveTx: the ride gone
// (domain.ErrNotFound) or the row moved since the snapshot (the CAS lost —
// domain.ErrInvalidTransition). Rides-only read: no cross-table facts are
// left to name at this point — the round guard is the port's, checked before
// the write; the arrival facts are state and version.
func (r *RideRepository) classifyArrivalRefusal(ctx context.Context, rideID, claimedVersion int) error {
	var (
		state   domain.RideState
		version int
	)

	err := r.pool.QueryRow(ctx, `SELECT state, version FROM rides WHERE id = $1`, rideID).Scan(&state, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrNotFound
	case err != nil:
		return err
	case version != claimedVersion:
		return domain.ErrInvalidTransition
	default:
		// A snapshot-before-commit read (same as classifyDecisionRefusal
		// saw): a refusal without a cause must not pass as a named one.
		return fmt.Errorf("ride arrival refused while ride %d at version %d", rideID, claimedVersion)
	}
}

// ridesMoneyStateCoupling is the declarative money-state-coupling gate
// (000013): unlocked|lost arrivals carry no accumulated bonus_acc — the
// predicate the domain's Unlock and Lost obey. It is row-local (references
// only NEW), so a CHECK constraint owns it instead of trigger check 4, per
// the Postgres hierarchy: declarative constraint above trigger whenever the
// constraint can express the rule. The adapter maps it by name, not by bare
// SQLSTATE: the rides table carries five other CHECKs that must keep falling
// through unhandled. Change-detector: the domain copies live in
// domain.Ride.Unlock/Lost, the intent copies in the ledger (ReleaseBonus
// before every unlocked|lost arrival — the player command and both
// settlement handlers); the DB probe is TestRideMoneyStateCoupling
// (internal/game/internal/core/service/constraints_test.go).
const ridesMoneyStateCoupling = "rides_money_state_coupling"

// decisionUpdateSQL is the ride commands' guarded decision write (Phase 04,
// ADR-024): the contended facts of the RIDE ROW in the WHERE, so check and
// write are one statement under READ COMMITTED:
//   - state = 'won_pending' — Lock/Burn/Unlock's only source state (ADR-016)
//   - the version the command read (the CAS against a concurrent ride
//     command)
//
// The ROUND facts that used to be two EXISTS predicates here (own round
// still open; destination live for continuations) are the Schedule port's
// gates now (RoundOpenTx / DestinationOpenTx): the calendar's own FOR SHARE
// statements run inside the command transaction BEFORE this write, which
// gives the same write-instant serialization the in-statement EXISTS gave.
// The rides row alone is touched by this statement.
//
// Money columns are absent from the SET clause since the ledger seam: the
// state machine moves state, continuation and streak; the ledger moves money
// (ReleaseBonus runs before the unlocked arrival, in the same tx).
const decisionUpdateSQL = `
		UPDATE rides
		SET match_id = $1, state = $2, streak = $3, version = version + 1
		WHERE id = $4 AND version = $5 AND state = 'won_pending'
		RETURNING id, player_id, team_id, created_at, state, tokens_locked,
			base_at_lock, bonus_acc, streak, version
	`

// DecisionGuardedTx writes a ride decision (Lock, Burn, Unlock) under the
// decisionUpdateSQL guard, on the caller's transaction — every ride command
// is idempotent (ADR-024), so the write shares the claim's transaction:
//   - the ride arrives post-mutation (Lock already re-pointed MatchID and
//     bumped the streak); the WHERE re-verifies the pre-mutation facts
//   - the round/destination gates are the use case's, run before this call
//   - 0 rows is the CAS lost to a concurrent ride command — classified after
//     the fact by classifyDecisionRefusal; rides_state_gate P0001 and
//     rides_money_state_coupling 23514 -> ErrInvalidTransition; a
//     destination deleted between the port gate and this write surfaces as
//     the FK 23503 -> ErrNotFound
func (r *RideRepository) DecisionGuardedTx(ctx context.Context, tx pgx.Tx, ride domain.Ride) (domain.Ride, error) {
	err := tx.QueryRow(ctx, decisionUpdateSQL,
		ride.MatchID, ride.State, ride.Streak, ride.ID, ride.Version,
	).Scan(
		&ride.ID,
		&ride.PlayerID,
		&ride.TeamID,
		&ride.CreatedAt,
		&ride.State,
		&ride.TokensLocked,
		&ride.BaseAtLock,
		&ride.Acc,
		&ride.Streak,
		&ride.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.Ride{}, r.classifyDecisionRefusal(ctx, ride.ID, ride.Version)
		case persistence.IsTriggerViolation(err):
			return domain.Ride{}, domain.ErrInvalidTransition
		case persistence.IsCheckViolation(err, ridesMoneyStateCoupling):
			// rides_money_state_coupling (000013): the declarative check
			// that took trigger check 4's job — unlocked|lost arrivals
			// carry no acc. Same 409 surface the P0001 refusal produced.
			return domain.Ride{}, domain.ErrInvalidTransition
		case persistence.IsFKViolation(err):
			// Since 000016 no FK is left on rides; the port's destination
			// gate has the match-existence check before this write. Kept as
			// the generic FK race mapping shape.
			return domain.Ride{}, domain.ErrNotFound
		default:
			return domain.Ride{}, err
		}
	}

	return ride, nil
}

// classifyDecisionRefusal names the 0-row causes of the decision write: the
// ride gone mid-flight (ErrNotFound, the parent-gone mapping) and the CAS
// lost to a concurrent ride command (ErrInvalidTransition — the claimed
// won_pending/version pre-state no longer exists on the row). Rides-only
// read: the round facts were the port's gates, checked before the write, so
// a refusal here cannot be the round's. The read runs on the pool beside the
// tx (per-statement snapshot): the refusal already happened, the read only
// names the reason, and it may legally see a snapshot from before the
// concurrent commit — the default branch catches that no-cause read instead
// of passing it as a named one.
func (r *RideRepository) classifyDecisionRefusal(ctx context.Context, rideID, claimedVersion int) error {
	var (
		state   domain.RideState
		version int
	)

	err := r.pool.QueryRow(ctx, `SELECT state, version FROM rides WHERE id = $1`, rideID).Scan(&state, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The ride itself is gone mid-flight: same parent-gone mapping the
		// FK refusals use.
		return domain.ErrNotFound
	case err != nil:
		return err
	case version != claimedVersion || state != domain.RideWonPending:
		// The CAS lost: a concurrent ride command moved the row — the
		// caller's won_pending claim no longer exists on it. The state half
		// is belt-and-braces (every ride command bumps the version), but a
		// state flip without a version bump is a defect either way.
		return domain.ErrInvalidTransition
	default:
		// A 0-row write with every checked predicate passing on this read:
		// only reachable when the read saw a snapshot from before the
		// concurrent commit (see above). A refusal without a cause must not
		// pass as a named one.
		return fmt.Errorf("ride decision refused while ride %d at version %d", rideID, claimedVersion)
	}
}

// ExistsForMatch reports whether any ride sits on the match — the game half
// of the calendar's match-delete refusal (000016: rides_match_id_fkey's
// RESTRICT is gone with the cross-context FKs, so the "match still has
// rides" check is a composition; ADR-018: a match with rides on it must not
// be deleted, the tokens would vanish with it).
func (r *RideRepository) ExistsForMatch(ctx context.Context, matchID int) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rides WHERE match_id = $1)`, matchID).Scan(&exists)
	return exists, err
}

// ExistsForTeam reports whether any ride names the team — the game half of
// the calendar's team-delete refusal (see ExistsForMatch; the
// rides_team_id_fkey RESTRICT is gone with the cross-context FKs).
func (r *RideRepository) ExistsForTeam(ctx context.Context, teamID int) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rides WHERE team_id = $1)`, teamID).Scan(&exists)
	return exists, err
}
