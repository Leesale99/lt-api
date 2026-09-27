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
	Version      int             `json:"version"`
}

func ValidateRide(v *validator.Validator, ride Ride) {
	v.Check(ride.PlayerID > 0, "player_id", "must be provided")
	v.Check(ride.TeamID > 0, "team_id", "must be provided")
	v.Check(ride.MatchID > 0, "match_id", "must be provided")
	v.Check(ride.TokensLocked.GreaterThan(decimal.Zero), "token_locked", "must be greater than zero")
}

var rideStates = []RideState{RideLocked, RideWonPending, RideBurned, RideUnlocked, RideLost}

func ValidateRideState(v *validator.Validator, state RideState) {
	v.Check(validator.PermittedValue(state, rideStates...), "state", "must be one of: locked, won_pending, burned, unlocked, lost")
}

type RideStore struct {
	pool *pgxpool.Pool
}

// rides is the in-memory persistence stub — Phase 03 replaces it with the
// rides table. It starts empty; tests plant rides through Insert and reset
// it via resetRides (service_test.go), mirroring the DB cleanup().
var rides []Ride

// resetRides clears the ride stub. Test-only hygiene, mirroring the DB
// fixture's cleanup(): planted rides and their ever-growing IDs must not
// leak across subtests.
func resetRides() {
	rides = nil
}

func (s *RideStore) Insert(ctx context.Context, ride Ride) (Ride, error) {
	// Persistence facts only: the domain Create command owns the ADR-019
	// initial-state contract, so Insert stamps server-side values (ID,
	// created_at) and stores the ride exactly as the domain produced it.
	ride.ID = len(rides) + 1
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
	// Zero-valued filters are ignored; provided filters are ANDed.
	filteredRides := []Ride{}
	for _, ride := range rides {
		if id != 0 && ride.ID != id {
			continue
		}
		if playerID != 0 && ride.PlayerID != playerID {
			continue
		}
		if teamID != 0 && ride.TeamID != teamID {
			continue
		}
		if matchID != 0 && ride.MatchID != matchID {
			continue
		}
		if state != "" && ride.State != state {
			continue
		}
		filteredRides = append(filteredRides, ride)
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

var streakRate = decimal.NewFromFloat(0.20)

func calculateBonus(matchOdds, tokensLocked, acc decimal.Decimal, streak int) decimal.Decimal {
	s := decimal.NewFromInt(int64(streak))
	one := decimal.NewFromInt(1)

	accDelta := tokensLocked.Mul(matchOdds.Sub(one).Mul(s.Mul(streakRate).Add(one)))

	return acc.Add(accDelta)
}
