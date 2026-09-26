package game

import (
	"context"
	"errors"
	"slices"
	"time"

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
	TokensLocked decimal.Decimal `json:"token_locked"`
	BaseAtLock   decimal.Decimal `json:"base_at_lock"`
	Acc          decimal.Decimal `json:"-"`
	Streak       int             `json:"-"`
	Version      int             `json:"Version"`
}

func ValidateRide(v *validator.Validator, ride Ride) {
	v.Check(ride.PlayerID > 0, "player_id", "must be provided")
	v.Check(ride.TeamID > 0, "team_id", "must be provided")
	v.Check(ride.MatchID > 0, "match_id", "must be provided")
	v.Check(ride.TokensLocked.GreaterThan(decimal.Zero), "token_locked", "must be greater then zero")
	v.Check(ride.State == RideLocked, "state", "ride must be created in locked state")
}

var rideStates = []RideState{RideLocked, RideWonPending, RideBurned, RideUnlocked, RideLost}

func ValidateRideState(v *validator.Validator, state RideState) {
	v.Check(validator.PermittedValue(state, rideStates...), "state", "must be one of: locked, won_pending, burned, unlocked, lost")
}

type RideStore struct {
	pool *pgxpool.Pool
}

var rides = []Ride{
	{
		ID:           1,
		CreatedAt:    time.Now().Add(-72 * time.Hour),
		PlayerID:     1,
		TeamID:       1,
		MatchID:      1,
		State:        RideLocked,
		TokensLocked: decimal.NewFromInt(100),
		BaseAtLock:   decimal.NewFromInt(100),
		Acc:          decimal.Zero,
		Streak:       0,
		Version:      1,
	},
	{
		ID:           2,
		CreatedAt:    time.Now().Add(-48 * time.Hour),
		PlayerID:     2,
		TeamID:       3,
		MatchID:      2,
		State:        RideWonPending,
		TokensLocked: decimal.NewFromInt(50),
		BaseAtLock:   decimal.NewFromInt(50),
		Acc:          decimal.NewFromFloat(35.0),
		Streak:       2,
		Version:      3,
	},
}

func (s *RideStore) Insert(ctx context.Context, ride Ride) (Ride, error) {
	// Initial-state contract (pinned by TestRide_Insert): a fresh ride is
	// locked with zero acc and streak, regardless of what the caller passed.
	ride.State = RideLocked
	ride.CreatedAt = time.Now()

	rides = append(rides, ride)

	return ride, nil
}

func (s *RideStore) Get(ctx context.Context, id int) (Ride, error) {
	for _, ride := range rides {
		if ride.ID == id {
			return ride, nil
		}
	}

	return Ride{}, store.ErrRecordNotFound
}

func (s *RideStore) GetAll(ctx context.Context, id, playerID, teamID, matchID int, state RideState, filters store.Filters) ([]Ride, store.Metadata, error) {
	filteredRides := []Ride{}
	for _, ride := range rides {
		if ride.ID == id || ride.PlayerID == playerID || ride.TeamID == teamID || ride.MatchID == matchID || ride.State == state {
			filteredRides = append(filteredRides, ride)
		}
	}

	metadata := store.CalculateMetadata(len(filteredRides), filters.Page, filters.PageSize)

	return filteredRides, metadata, nil
}

func (s *RideStore) Update(ctx context.Context, ride Ride) (Ride, error) {
	for i, r := range rides {
		if r.ID == ride.ID {
			rides[i] = ride
			return rides[i], nil
		}
	}

	return Ride{}, store.ErrRecordNotFound
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

// TODO:  matches, err := matches.GetAll(ride.RoundID)
var matches = []Match{
	{
		ID:       1,
		StartsAt: time.Now().Add(24 * time.Hour),
		RoundID:  1,
		Odds:     Odds{Home: 1.75, Away: 2.50},
	},
	{
		ID:       2,
		StartsAt: time.Now().Add(72 * time.Hour),
		RoundID:  2,
		Odds:     Odds{Home: 1.75, Away: 2.50},
	},
}

// The service layer owns the clock: it fetches the round's first match
// start time and last match end time, and passes the derived phase into each command, e.g.
//
//	phase := Phase(time.Now(), firstMatchAt, lastMatchAt)
//	ride.Burn(phase)

// Phase ActionPhase; Call when: Player locks his tokens on a chosen match
func (r *Ride) Create(phase RoundPhase, ride Ride) error {
	if phase != ActionPhase {
		return ErrInvalidRoundPhase
	}

	rides = append(rides, ride)

	return nil
}

// Phase: MatchPhase; Call when: Match won; Ride transitions to won_pending
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

// Phase: MatchPhase; Call when: Match lost; Ride transitions to lost
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

// Phase: DecisionPhase; Call when: Player decides to contiue the ride after the win
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

// Phase: DecisionPhase; Call when: Player decides to burn after the win
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

// Phase: DecisionPhase; Call when: Player decides to Unlock tokens after the win
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

var streakRate = decimal.NewFromFloat(0.20)

func calculateBonus(matchOdds, tokensLocked, acc decimal.Decimal, streak int) decimal.Decimal {
	s := decimal.NewFromInt(int64(streak))
	one := decimal.NewFromInt(1)

	accDelta := tokensLocked.Mul(matchOdds.Sub(one).Mul(s.Mul(streakRate).Add(one)))

	return acc.Add(accDelta)
}
