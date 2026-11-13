package game

// rides.go persists the ride rows and ONLY the ride rows: since the Phase C
// split every schedule fact reaches game through the SchedulePort (round
// guards) or the event payloads (results), so there is no SQL left here
// touching matches or rounds. The money columns (tokens_locked, bonus_acc)
// are the Ledger's — this file writes state, continuation and streak; the
// birth INSERT is the one exception (a ride is born carrying its locked
// tokens — see the insert statements).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

// ErrInvalidTransition rejects a command invalid from the ride's current
// state: terminal states accept nothing, and re-issuing a command whose
// state has already moved on.
var ErrInvalidTransition = errors.New("invalid state transition")

type RideState string

const (
	RideLocked     RideState = "locked"
	RideWonPending RideState = "won_pending"
	RideBurned     RideState = "burned"
	RideUnlocked   RideState = "unlocked"
	RideLost       RideState = "lost"
)

type Ride struct {
	ID           int             `json:"id"`
	CreatedAt    time.Time       `json:"-"`
	PlayerID     int             `json:"player_id"`
	TeamID       int             `json:"team_id"`
	MatchID      int             `json:"match_id"`
	State        RideState       `json:"state"`
	TokensLocked decimal.Decimal `json:"tokens_locked"`
	BaseAtLock   decimal.Decimal `json:"base_at_lock"`
	Acc          decimal.Decimal `json:"-"`
	Streak       int             `json:"-"`
	Version      int             `json:"version"`
}

func ValidateRide(v *validator.Validator, ride Ride) {
	v.Check(ride.PlayerID > 0, "player_id", "must be provided")
	v.Check(ride.TeamID > 0, "team_id", "must be provided")
	v.Check(ride.MatchID > 0, "match_id", "must be provided")
	v.Check(ride.TokensLocked.GreaterThan(decimal.Zero), "tokens_locked", "must be greater than zero")
}

var rideStates = []RideState{RideLocked, RideWonPending, RideBurned, RideUnlocked, RideLost}

func ValidateRideState(v *validator.Validator, state RideState) {
	v.Check(validator.PermittedValue(state, rideStates...), "state", "must be one of: locked, won_pending, burned, unlocked, lost")
}

type RideStore struct {
	pool *pgxpool.Pool
}

// Insert admits locked only: rides are created locked (ADR-019), and
// since 000012 the rides_state_insert_gate refuses every other state —
// all other states must be reached through a gated UPDATE. The domain's
// ride.Create (the only production caller) stamps RideLocked before
// this runs.
func (s *RideStore) Insert(ctx context.Context, ride Ride) (Ride, error) {
	return insert(ctx, s.pool, ride)
}

// insertSQL is the ride's birth statement: one row, born locked with its
// initial money columns (tokens_locked, base_at_lock, bonus_acc) — the
// rides_tokens_locked_check admits no zero-token birth (ADR-019: no
// DEFAULT, the initial-state contract is ride.Create's), so the lock and
// the row are one statement. The round-open fact that used to live in this
// statement's SELECT (the FOR SHARE guard) is the SchedulePort's
// RoundOpenTx gate now: the service pins the round row inside the command
// transaction before this INSERT runs.
const insertSQL = `
		INSERT INTO rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, version
	`

// insert runs the birth statement on any querier — the pool (Insert, the
// test-planter path) or the caller's transaction (InsertTx, the idempotent
// command path).
func insert(ctx context.Context, q store.Querier, ride Ride) (Ride, error) {
	args := []any{ride.PlayerID, ride.TeamID, ride.MatchID, ride.State, ride.TokensLocked, ride.BaseAtLock, ride.Acc, ride.Streak}

	err := q.QueryRow(ctx, insertSQL, args...).Scan(&ride.ID, &ride.CreatedAt, &ride.Version)
	if err != nil {
		switch {
		case store.IsFKViolation(err):
			// The referenced player, team or match does not exist — the
			// handler validates only positivity, so an unknown parent can
			// only be caught here (same convention as PlayerStore.Insert).
			return Ride{}, store.ErrRecordNotFound
		case store.IsTriggerViolation(err):
			// rides_state_insert_gate: a non-locked state tried to be born
			// (ADR-019). Same 409 surface as every other gate refusal.
			return Ride{}, ErrInvalidTransition
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// InsertTx writes the created ride on the caller's transaction —
// RideCreate is idempotent (ADR-024), so the write shares the claim's
// transaction. The ride arrives post-Create — state, acc and streak are
// the domain command's (ADR-019) — and the ride's round-open fact was
// pinned by the service through SchedulePort.RoundOpenTx BEFORE this runs
// (the FOR SHARE lock is held until the command tx commits, the same
// serialization the old in-statement guard provided):
//   - unknown player/team surface as the FK 23503 -> ErrRecordNotFound,
//     same mapping as Insert
//   - a non-locked state is the 000012 insert gate -> ErrInvalidTransition
func (s *RideStore) InsertTx(ctx context.Context, tx pgx.Tx, ride Ride) (Ride, error) {
	return insert(ctx, tx, ride)
}

// LockedForMatchTx reads the locked rides of one match, on the caller's
// transaction — the settlement handlers' scan. The match id comes from the
// event payload; no join: rides owns the match_id column (ADR-018).
func (s *RideStore) LockedForMatchTx(ctx context.Context, tx pgx.Tx, matchID int) ([]Ride, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak, created_at, version
		FROM rides
		WHERE match_id = $1 AND state = 'locked'
		ORDER BY id
	`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rides := []Ride{}
	for rows.Next() {
		var ride Ride
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

// UndecidedForMatchesTx reads the undecided (won_pending) rides of the
// given matches, on the caller's transaction — the round settlement's
// sweep. The match ids come from the RoundCompleted payload; no join.
func (s *RideStore) UndecidedForMatchesTx(ctx context.Context, tx pgx.Tx, matchIDs []int) ([]Ride, error) {
	if len(matchIDs) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT id, player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak, created_at, version
		FROM rides
		WHERE match_id = ANY($1) AND state = 'won_pending'
		ORDER BY id
	`, matchIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rides := []Ride{}
	for rows.Next() {
		var ride Ride
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

func (s *RideStore) Get(ctx context.Context, id int) (Ride, error) {
	query := `
		SELECT id, player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak, created_at, version
		FROM rides
		WHERE id = $1
	`
	var ride Ride

	err := s.pool.QueryRow(ctx, query, id).Scan(
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
			return Ride{}, store.ErrRecordNotFound
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

func (s *RideStore) GetAll(ctx context.Context, id, playerID, teamID, matchID int, state RideState, filters store.Filters) ([]Ride, store.Metadata, error) {
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

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, store.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	rides := []Ride{}

	for rows.Next() {
		var ride Ride

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
			return nil, store.Metadata{}, err
		}

		rides = append(rides, ride)
	}

	if err = rows.Err(); err != nil {
		return nil, store.Metadata{}, err
	}

	metadata := store.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return rides, metadata, nil
}

// arrivalUpdateSQL is the settlement handlers' arrival write (Phase C,
// behind the MatchResultRecorded/RoundCompleted events): a single-ride
// state flip whose WHERE re-checks the contended facts at the write
// instant — the ride's version and its from-state — so a concurrent writer
// (a player decision that landed first, a settlement replay) turns the
// second write into a 0-row no-op. No match join: the destination is
// derived from the event payload and verified against the committed
// result by the service's resultAgreement check before this write (the
// app-level enforcement home of the old rides_state_gate check 5 since
// migration 000017 — the DB no longer reads across the boundary).
//
// Money columns are deliberately absent from the SET clause: the release
// of the accrued bonus (unlocked|lost arrivals) and the accrual (won
// arrivals) are the Ledger's writes, ordered before/after this flip inside
// the same transaction (rides_money_state_coupling (000013) couples the
// unlocked|lost arrivals to zero acc, so the release must precede the
// flip).
const arrivalUpdateSQL = `
		UPDATE rides
		SET state = $2, version = version + 1
		WHERE id = $1 AND version = $3 AND state = $4
		RETURNING id, player_id, team_id, created_at, state, tokens_locked,
			base_at_lock, bonus_acc, streak, version
	`

// ArriveTx writes one settlement arrival (from-state → to-state) on the
// caller's transaction. 0 rows is classified after the fact
// (classifyArrivalRefusal): a ride gone mid-tx is ErrRecordNotFound, a
// moved row is ErrInvalidTransition — in the single-settlement world of
// the claimed event, both are bug-shaped.
func (s *RideStore) ArriveTx(ctx context.Context, tx pgx.Tx, rideID int, from, to RideState, claimedVersion int) (Ride, error) {
	var ride Ride

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
			return Ride{}, s.classifyArrivalRefusal(ctx, rideID, claimedVersion)
		case store.IsTriggerViolation(err):
			// rides_state_gate (ADR-020): the DB refused the arrival — the
			// state graph (checks 1–3); result agreement is the service's
			// app-level check since migration 000017.
			return Ride{}, ErrInvalidTransition
		case store.IsCheckViolation(err, ridesMoneyStateCoupling):
			return Ride{}, ErrInvalidTransition
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// classifyArrivalRefusal names the 0-row causes of ArriveTx: the ride gone
// (ErrRecordNotFound) or the row moved since the snapshot (the CAS lost —
// ErrInvalidTransition). Rides-only read: no cross-table facts are left to
// name at this point — the round guard is the port's, checked before the
// write; the arrival facts are state and version.
func (s *RideStore) classifyArrivalRefusal(ctx context.Context, rideID, claimedVersion int) error {
	var (
		state   RideState
		version int
	)

	err := s.pool.QueryRow(ctx, `SELECT state, version FROM rides WHERE id = $1`, rideID).Scan(&state, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.ErrRecordNotFound
	case err != nil:
		return err
	case version != claimedVersion:
		return ErrInvalidTransition
	default:
		// A snapshot-before-commit read (same as classifyUpdateRefusal
		// saw): a refusal without a cause must not pass as a named one.
		return fmt.Errorf("ride arrival refused while ride %d at version %d", rideID, claimedVersion)
	}
}

func (s *RideStore) Update(ctx context.Context, ride Ride) (Ride, error) {
	// bonus_acc is deliberately absent from the SET clause: the money
	// columns are the Ledger's since Phase C — this statement moves state,
	// continuation and streak; the ledger moves money (and the money-state
	// coupling still checks the row as a whole).
	query := `
		UPDATE rides
		SET match_id = $1, state = $2, streak = $3, version = version + 1
		WHERE id = $4 AND version = $5
		RETURNING id, player_id, team_id, version
	`
	args := []any{ride.MatchID, ride.State, ride.Streak, ride.ID, ride.Version}

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&ride.ID,
		&ride.PlayerID,
		&ride.TeamID,
		&ride.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Ride{}, store.ErrEditConflict
		case store.IsTriggerViolation(err):
			// rides_state_gate (ADR-020): the DB refused the transition.
			// Deliberately ErrInvalidTransition — the invalid-state 409 —
			// NOT ErrRecordInUse: the row is not referenced by anything,
			// its state machine rejected the write.
			return Ride{}, ErrInvalidTransition
		case store.IsCheckViolation(err, ridesMoneyStateCoupling):
			// rides_money_state_coupling (000013): the declarative check
			// that took trigger check 4's job — unlocked|lost arrivals
			// carry no acc. Same 409 surface the P0001 refusal produced.
			return Ride{}, ErrInvalidTransition
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// ridesMoneyStateCoupling is the declarative money-state-coupling gate
// (000013): unlocked|lost arrivals carry no accumulated bonus_acc — the
// predicate ride.Unlock and ride.Lost obey in the domain. It is row-local
// (references only NEW), so a CHECK constraint owns it instead of trigger
// check 4, per the Postgres hierarchy: declarative constraint above
// trigger whenever the constraint can express the rule. The store maps it
// by name, not by bare SQLSTATE: the rides table carries five other
// CHECKs that must keep falling through unhandled. Change-detector: the
// domain copies live in ride.Unlock/ride.Lost, the intent copies in the
// Ledger (ReleaseBonus before every unlocked|lost arrival — the player
// command and both settlement handlers); the DB probe is
// TestRideMoneyStateCoupling (internal/game/constraints_test.go).
const ridesMoneyStateCoupling = "rides_money_state_coupling"

// decisionUpdateSQL is the ride commands' guarded decision write (Phase 04,
// ADR-024): the contended facts of the RIDE ROW in the WHERE, so check and
// write are one statement under READ COMMITTED:
//   - state = 'won_pending' — Lock/Burn/Unlock's only source state (ADR-016)
//   - the version the command read (the CAS against a concurrent ride
//     command)
//
// The ROUND facts that used to be two EXISTS predicates here (own round
// still open; destination live for continuations) are the SchedulePort's
// gates now (RoundOpenTx / DestinationOpenTx): schedule's own FOR SHARE
// statements run inside the command transaction BEFORE this write, which
// gives the same write-instant serialization the in-statement EXISTS gave —
// the port's lock-taking re-reads the newest committed rows after any
// concurrent close/resolve commits, and the write runs only if the gates
// passed. The rides row alone is touched by this statement.
//
// Money columns are absent from the SET clause since the ledger seam: the
// state machine moves state, continuation and streak; the ledger moves
// money (ReleaseBonus runs before the unlocked arrival, in the same tx).
// rides_money_state_coupling (000013) still couples the arrivals to zero
// acc at the row — on whatever the ledger wrote.
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
//   - the round/destination gates are the service's, run before this call
//   - 0 rows is the CAS lost to a concurrent ride command (the claimed
//     pre-state no longer exists on the row) — classified after the fact
//     by classifyDecisionRefusal; rides_state_gate P0001 and
//     rides_money_state_coupling 23514 -> ErrInvalidTransition; a
//     destination deleted between the port gate and this write surfaces as
//     the FK 23503 -> ErrRecordNotFound
func (s *RideStore) DecisionGuardedTx(ctx context.Context, tx pgx.Tx, ride Ride) (Ride, error) {
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
			return Ride{}, s.classifyDecisionRefusal(ctx, ride.ID, ride.Version)
		case store.IsTriggerViolation(err):
			return Ride{}, ErrInvalidTransition
		case store.IsCheckViolation(err, ridesMoneyStateCoupling):
			// rides_money_state_coupling (000013): the declarative check
			// that took trigger check 4's job — unlocked|lost arrivals
			// carry no acc. Same 409 surface the P0001 refusal produced.
			return Ride{}, ErrInvalidTransition
		case store.IsFKViolation(err):
			// Since Phase D (000016) no FK is left on rides; the port's
			// destination gate has the match-existence check before this
			// write. Kept as the generic FK race mapping shape.
			return Ride{}, store.ErrRecordNotFound
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// classifyDecisionRefusal names the 0-row causes of the decision write:
// the ride gone mid-flight (ErrRecordNotFound, the parent-gone mapping)
// and the CAS lost to a concurrent ride command (ErrInvalidTransition,
// the invalid-state 409 — the claimed won_pending/version pre-state no
// longer exists on the row). Rides-only read: the round facts were the
// port's gates, checked before the write, so a refusal here cannot be the
// round's. The read runs on the pool beside the tx (per-statement
// snapshot): the refusal already happened, the read only names the reason,
// and it may legally see a snapshot from before the concurrent commit —
// the default branch catches that no-cause read instead of passing it as
// a named one.
func (s *RideStore) classifyDecisionRefusal(ctx context.Context, rideID, claimedVersion int) error {
	var (
		state   RideState
		version int
	)

	err := s.pool.QueryRow(ctx, `SELECT state, version FROM rides WHERE id = $1`, rideID).Scan(&state, &version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The ride itself is gone mid-flight: same parent-gone mapping
		// the FK refusals use.
		return store.ErrRecordNotFound
	case err != nil:
		return err
	case version != claimedVersion || state != RideWonPending:
		// The CAS lost: a concurrent ride command moved the row — the
		// caller's won_pending claim no longer exists on it. The state
		// half is belt-and-braces (every ride command bumps the version),
		// but a state flip without a version bump is a defect either way.
		return ErrInvalidTransition
	default:
		// A 0-row write with every checked predicate passing on this
		// read: only reachable when the read saw a snapshot from before
		// the concurrent commit (see above). A refusal without a cause
		// must not pass as a named one.
		return fmt.Errorf("ride decision refused while ride %d at version %d", rideID, claimedVersion)
	}
}

// ExistsForMatch reports whether any ride sits on the match — the game
// half of the api match-delete refusal (Phase D: rides_match_id_fkey's
// RESTRICT is gone with the cross-context FKs, so the "match still has
// rides" check is an api-level composition; same ErrRecordInUse → 409
// surface the FK produced, ADR-018: a match with rides on it must not be
// deleted, the tokens would vanish with it).
func (s *RideStore) ExistsForMatch(ctx context.Context, matchID int) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rides WHERE match_id = $1)`, matchID).Scan(&exists)
	return exists, err
}

// ExistsForTeam reports whether any ride names the team — the game half
// of the api team-delete refusal (see ExistsForMatch; the
// rides_team_id_fkey RESTRICT is gone with the cross-context FKs).
func (s *RideStore) ExistsForTeam(ctx context.Context, teamID int) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rides WHERE team_id = $1)`, teamID).Scan(&exists)
	return exists, err
}

var transitions = map[RideState]map[schedule.RoundPhase][]RideState{
	RideLocked: {
		schedule.MatchPhase: {RideWonPending, RideLost},
	},
	RideWonPending: {
		schedule.DecisionPhase: {RideLocked, RideBurned, RideUnlocked},
	},
}

func canTransition(state RideState, phase schedule.RoundPhase, next RideState) bool {
	allowedStates := transitions[state][phase]

	return slices.Contains(allowedStates, next)
}

// The service layer owns the clock: it fetches the round's first match
// start time and last match end time, and passes the derived phase into each command, e.g.
//
//	phase := Phase(time.Now(), firstMatchAt, lastMatchAt)
//	ride.Burn(phase)

// Create locks the ride on a chosen match. Phase: ActionPhase; call when: Player decides to lock tokens on a chosen match.
// The creation command is the single enforcement point for the ADR-019
// initial-state contract: it overwrites state, acc and streak, so an
// invalid initial state cannot be produced through normal domain
// operations — whatever the caller supplied is discarded. Persistence is
// the store's job.
func (r *Ride) Create(phase schedule.RoundPhase) error {
	if phase != schedule.ActionPhase {
		return ErrInvalidRoundPhase
	}

	r.State = RideLocked
	r.Acc = decimal.Zero
	r.Streak = 0

	return nil
}

// WonPending records a won match. Phase: MatchPhase; call when: Match won; Ride transitions to won_pending
func (r *Ride) WonPending(phase schedule.RoundPhase, odds decimal.Decimal) error {
	if phase != schedule.MatchPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideWonPending) {
		return ErrInvalidTransition
	}

	r.State = RideWonPending
	r.Acc = calculateBonus(odds, r.TokensLocked, r.Acc, r.Streak)

	return nil
}

// Lost records a lost match. Phase: MatchPhase; call when: Match lost; Ride transitions to lost
func (r *Ride) Lost(phase schedule.RoundPhase) error {
	if phase != schedule.MatchPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideLost) {
		return ErrInvalidTransition
	}

	r.State = RideLost
	r.Acc = decimal.Zero

	return nil
}

// Lock continues the ride after the win. Phase: DecisionPhase; call when: Player decides to continue the ride after the win
func (r *Ride) Lock(phase schedule.RoundPhase, nextMatchID int) error {
	if phase != schedule.DecisionPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideLocked) {
		return ErrInvalidTransition
	}

	r.State = RideLocked
	r.MatchID = nextMatchID
	r.Streak = r.Streak + 1

	return nil
}

// Burn ends a won ride: state becomes RideBurned,
// tokens and accumulated bonus are fulfiled as TB. Phase: DecisionPhase; call when: Player decides to burn after the win
func (r *Ride) Burn(phase schedule.RoundPhase) error {
	if phase != schedule.DecisionPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideBurned) {
		return ErrInvalidTransition
	}

	r.State = RideBurned

	return nil
}

// Unlock ends a won ride: state becomes RideUnlocked and the accumulated
// bonus is cleared. Phase: DecisionPhase; call when: Player decides to Unlock tokens after the win
func (r *Ride) Unlock(phase schedule.RoundPhase) error {
	if phase != schedule.DecisionPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideUnlocked) {
		return ErrInvalidTransition
	}

	r.State = RideUnlocked
	r.Acc = decimal.Zero

	return nil
}

// streakRate is a ≤2-dp decimal by contract (ADR-021): the bonus scale chain
// in the rides schema assumes the multiplier 1 + streakRate×streak never
// carries more than 2 decimal places. Tuning beyond that budget re-opens
// silent rounding — declare the new budget in the ADR and widen bonus_acc
// before changing this constant.
var streakRate = decimal.NewFromFloat(0.20)

// bonusDelta is the accrued amount of one won match: the increment the
// Ledger's AccrueBonus applies (the settlement handler's input). Kept
// separate from calculateBonus so the accrual total and its delta share
// one formula.
func bonusDelta(matchOdds, tokensLocked decimal.Decimal, streak int) decimal.Decimal {
	s := decimal.NewFromInt(int64(streak))
	one := decimal.NewFromInt(1)

	return tokensLocked.Mul(matchOdds.Sub(one).Mul(s.Mul(streakRate).Add(one)))
}

func calculateBonus(matchOdds, tokensLocked, acc decimal.Decimal, streak int) decimal.Decimal {
	return acc.Add(bonusDelta(matchOdds, tokensLocked, streak))
}
