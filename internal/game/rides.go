package game

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

func (s *RideStore) Insert(ctx context.Context, ride Ride) (Ride, error) {
	query := `
		INSERT INTO rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, version
	`
	args := []any{
		ride.PlayerID,
		ride.TeamID,
		ride.MatchID,
		ride.State,
		ride.TokensLocked,
		ride.BaseAtLock,
		ride.Acc,
		ride.Streak,
	}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&ride.ID, &ride.CreatedAt, &ride.Version)
	if err != nil {
		switch {
		case store.IsFKViolation(err):
			// The referenced player, team or match does not exist — the
			// handler validates only positivity, so an unknown parent can
			// only be caught here (same convention as PlayerStore.Insert).
			return Ride{}, store.ErrRecordNotFound
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// guardedInsertSQL is RideCreate's guarded write (Phase 04 TOCTOU sweep,
// ADR-024): an INSERT has no prior row, so there is no version column to
// piggyback on — the guard moves onto the source SELECT, and the ride is
// written only if its match's round is still open at the write instant.
//
// The SELECT takes FOR SHARE on the round row. Under READ COMMITTED a
// statement's snapshot is taken once at its start, so a bare
// INSERT ... SELECT would still let a concurrent close commit after that
// snapshot and land a live ride inside a closed round — the exact defect
// this sweep removes. The row lock makes the close's guarded flip on the
// round row and this INSERT serialize in either order: if the close holds
// the lock, this SELECT waits, then re-reads the newest committed row and
// re-evaluates the WHERE (EvalPlanQual — the same mechanism
// guardedUpdateSQL rides from the write side) and the flip to closed
// rejects the insert with 0 rows. Create has exactly one contended fact
// (round status), so "pins only one of three facts" does not apply.
//
// With the idempotency claim (ADR-024) the insert shares the command
// transaction, so the SHARE lock is held until the claim tx commits — not
// merely for the statement. The tx's other writes (the claim, the stored
// response) touch only idempotency_keys, never rounds: the serialization
// against the close stays exactly what the guard promises.
const guardedInsertSQL = `
		INSERT INTO rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8
		FROM matches m
		INNER JOIN rounds r ON r.id = m.round_id
		WHERE m.id = $3 AND r.status = 'open'
		FOR SHARE OF r
		RETURNING id, created_at, version
	`

// InsertGuardedTx writes the created ride under the guardedInsertSQL guard,
// on the caller's transaction — RideCreate is idempotent (ADR-024), so the
// write shares the claim's transaction. The ride arrives post-Create —
// state, acc and streak are the domain command's (ADR-019) — and the
// round-open fact is re-checked in the same statement:
//   - 0 rows is the race lost (the round closed under us) ->
//     ErrRoundNotOpen, the round-refusal 409; the rowcount also covers an
//     unknown match, which is classified with one follow-up read
//   - unknown player/team still surface as the FK 23503 ->
//     ErrRecordNotFound, same mapping as Insert
func (s *RideStore) InsertGuardedTx(ctx context.Context, tx pgx.Tx, ride Ride) (Ride, error) {
	args := []any{ride.PlayerID, ride.TeamID, ride.MatchID, ride.State, ride.TokensLocked, ride.BaseAtLock, ride.Acc, ride.Streak}

	err := tx.QueryRow(ctx, guardedInsertSQL, args...).Scan(&ride.ID, &ride.CreatedAt, &ride.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Ride{}, s.classifyInsertRefusal(ctx, ride.MatchID)
		case store.IsFKViolation(err):
			return Ride{}, store.ErrRecordNotFound
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// classifyInsertRefusal distinguishes the two 0-row causes of the guarded
// insert: an unknown match (ErrRecordNotFound, the FK mapping Insert gives)
// and a round that is not open (ErrRoundNotOpen, the round-refusal 409).
// Both facts are settled by the time this runs — the write already did not
// happen — so this read classifies the refusal, it does not gate anything.
// It deliberately reads on the pool beside the tx (per-statement snapshot):
// the refusal already happened, the read only names the reason.
func (s *RideStore) classifyInsertRefusal(ctx context.Context, matchID int) error {
	query := `
		SELECT r.status
		FROM matches m
		INNER JOIN rounds r ON r.id = m.round_id
		WHERE m.id = $1
	`
	var status RoundStatus

	err := s.pool.QueryRow(ctx, query, matchID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.ErrRecordNotFound
	case err != nil:
		return err
	case status != RoundOpen:
		return ErrRoundNotOpen
	default:
		// Unreachable through the gates (a round cannot re-open), but a
		// refusal without a cause must not pass as success.
		return fmt.Errorf("ride insert refused while match %d round open", matchID)
	}
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

type Result struct {
	ID    int
	State RideState
}

// UpdateAllForMatchTx is ResolveMatch's ride half: one set-based statement
// moving every locked ride of the match to its score-derived state. The
// destination is a function of (match scores, ride.team_id) — computed in
// SQL from the source of truth (ADR-018), not shipped per-ride from Go: the
// UPDATE rides the join to matches, so one statement is one pass over the
// (match_id, state = 'locked') index range. A draw crowns no winner (the
// rule Match.Winner encodes in Go), so the CASE's ELSE lands every ride of
// a drawn match lost.
//
// It runs on the caller's transaction, AFTER MatchStore.ResolveTx has
// written the scores: rides_state_gate's result-agreement check reads the
// match row in this same tx and refuses (P0001 → ErrInvalidTransition) any
// ride whose destination disagrees with the scores — the gate enforces what
// the CASE merely computes. The state = 'locked' filter makes a
// re-resolution a 0-row no-op, which is the idempotency half of
// ResolveMatch's Phase 04 contract.
func (s *RideStore) UpdateAllForMatchTx(ctx context.Context, tx pgx.Tx, matchID int) ([]Result, error) {
	query := `
		UPDATE rides r
		SET state = CASE
			WHEN m.home_score > m.away_score AND r.team_id = m.home_team_id THEN 'won_pending'
			WHEN m.home_score < m.away_score AND r.team_id = m.away_team_id THEN 'won_pending'
			ELSE 'lost'
		END,
		version = version + 1
		FROM matches m
		WHERE m.id = $1 AND r.match_id = $1 AND r.state = 'locked'
		RETURNING r.id, r.state
	`
	rows, err := tx.Query(ctx, query, matchID)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var results []Result
	for rows.Next() {
		var result Result

		err := rows.Scan(&result.ID, &result.State)
		if err != nil {
			return nil, err
		}

		results = append(results, result)
	}

	// Trigger refusals surface here, not at Query: the statement fails while
	// the rows are being streamed, so the gate's P0001 arrives via rows.Err.
	if err := rows.Err(); err != nil {
		if store.IsTriggerViolation(err) {
			// rides_state_gate result agreement: some ride's destination
			// disagreed with the match's scores (ADR-020). The tx rolls back
			// upstream — scores and rides together.
			return nil, ErrInvalidTransition
		}
		return nil, err
	}

	return results, nil
}

func (s *RideStore) Update(ctx context.Context, ride Ride) (Ride, error) {
	query := `
		UPDATE rides
		SET match_id = $1, state = $2, bonus_acc = $3, streak = $4, version = version + 1
		WHERE id = $5 AND version = $6
		RETURNING id, player_id, team_id, version
	`
	args := []any{ride.MatchID, ride.State, ride.Acc, ride.Streak, ride.ID, ride.Version}

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
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

// guardedUpdateSQL is the ride commands' guarded write (Phase 04, ADR-024):
// the full-column shape of Update plus the contended facts in the WHERE, so
// check and write are one statement under READ COMMITTED:
//   - state = 'won_pending' — Lock/Burn/Unlock's only source state (ADR-016)
//   - the ride's own round is still open — cross-row (rides -> matches ->
//     rounds), not the version column alone
//   - for continuations ($7 = destinationMatchID > 0): destination match
//     un-ended, its round not closed — the predicate NextForTeam filters on
//
// A blocked UPDATE re-reads the newest committed row when the lock frees and
// re-evaluates this WHERE (EvalPlanQual — the round-close auto-unlock rides
// the same mechanism from the other side). 0 rows is the race lost, not a bug.
const guardedUpdateSQL = `
		UPDATE rides
		SET match_id = $1, state = $2, bonus_acc = $3, streak = $4, version = version + 1
		WHERE id = $5 AND version = $6
			AND state = 'won_pending'
			AND EXISTS (
				SELECT 1
				FROM matches om
				INNER JOIN rounds o ON o.id = om.round_id
				WHERE om.id = rides.match_id AND o.status = 'open'
			)
			AND (
				$7 = 0
				OR EXISTS (
					SELECT 1
					FROM matches nm
					INNER JOIN rounds n ON n.id = nm.round_id
					WHERE nm.id = $7 AND nm.ended_at IS NULL AND n.status <> 'closed'
				)
			)
		RETURNING id, player_id, team_id, created_at, state, tokens_locked,
			base_at_lock, bonus_acc, streak, version
	`

// UpdateGuardedTx writes a ride decision (Lock, Burn, Unlock) under the
// guardedUpdateSQL guard, on the caller's transaction — every ride command
// is idempotent (ADR-024), so the write shares the claim's transaction:
//   - the ride arrives post-mutation; the WHERE re-verifies pre-mutation facts
//   - destinationMatchID is the continuation's match, or 0 for Burn/Unlock
//   - 0 rows -> ErrRoundNotOpen (the round-refusal 409); rides_state_gate
//     P0001 -> ErrInvalidTransition (ADR-020); destination deleted
//     mid-flight -> ErrRecordNotFound
func (s *RideStore) UpdateGuardedTx(ctx context.Context, tx pgx.Tx, ride Ride, destinationMatchID int) (Ride, error) {
	err := tx.QueryRow(ctx, guardedUpdateSQL,
		ride.MatchID, ride.State, ride.Acc, ride.Streak, ride.ID, ride.Version, destinationMatchID,
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
			return Ride{}, ErrRoundNotOpen
		case store.IsTriggerViolation(err):
			return Ride{}, ErrInvalidTransition
		case store.IsFKViolation(err):
			// The destination match was deleted between NextForTeam's pick
			// and this write — same parent-gone mapping as Insert.
			return Ride{}, store.ErrRecordNotFound
		default:
			return Ride{}, err
		}
	}

	return ride, nil
}

var transitions = map[RideState]map[RoundPhase][]RideState{
	RideLocked: {
		MatchPhase: {RideWonPending, RideLost},
	},
	RideWonPending: {
		DecisionPhase: {RideLocked, RideBurned, RideUnlocked},
	},
}

func canTransition(state RideState, phase RoundPhase, next RideState) bool {
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
func (r *Ride) Create(phase RoundPhase) error {
	if phase != ActionPhase {
		return ErrInvalidRoundPhase
	}

	r.State = RideLocked
	r.Acc = decimal.Zero
	r.Streak = 0

	return nil
}

// WonPending records a won match. Phase: MatchPhase; call when: Match won; Ride transitions to won_pending
func (r *Ride) WonPending(phase RoundPhase, odds decimal.Decimal) error {
	if phase != MatchPhase {
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
func (r *Ride) Lost(phase RoundPhase) error {
	if phase != MatchPhase {
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
func (r *Ride) Lock(phase RoundPhase, nextMatchID int) error {
	if phase != DecisionPhase {
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
func (r *Ride) Burn(phase RoundPhase) error {
	if phase != DecisionPhase {
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
func (r *Ride) Unlock(phase RoundPhase) error {
	if phase != DecisionPhase {
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

func calculateBonus(matchOdds, tokensLocked, acc decimal.Decimal, streak int) decimal.Decimal {
	s := decimal.NewFromInt(int64(streak))
	one := decimal.NewFromInt(1)

	accDelta := tokensLocked.Mul(matchOdds.Sub(one).Mul(s.Mul(streakRate).Add(one)))

	return acc.Add(accDelta)
}
