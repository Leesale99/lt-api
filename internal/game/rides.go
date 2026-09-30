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

	return ride, err
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
		conds = append(conds, fmt.Sprintf("id = %d", len(args)))
	}
	if playerID != 0 {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("player_id = %d", len(args)))
	}
	if teamID != 0 {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("team_id = %d", len(args)))
	}
	if matchID != 0 {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("match_id = %d", len(args)))
	}
	if state != "" {
		args = append(args, id)
		conds = append(conds, fmt.Sprintf("state = %d", len(args)))
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

	totalRecors := 0
	rides := []Ride{}

	for rows.Next() {
		var ride Ride

		err := rows.Scan(
			&totalRecors,
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

	metadata := store.CalculateMetadata(totalRecors, filters.Page, filters.PageSize)

	return rides, metadata, nil
}

func (s *RideStore) Update(ctx context.Context, ride Ride) (Ride, error) {
	query := `
		UPDATE rides
	 	SET match_id = $1, state = $2, bonus_acc = $3, streak = $4, version + 1
		WHERE id = $5 AND version = $6
		RETURNING id, player_id, team_id, version
	`
	args := []any{ride.MatchID, ride.State, ride.Acc, ride.Streak}

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
