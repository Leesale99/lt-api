package game

import (
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

type Decimal = decimal.Decimal

type RideState string

const (
	RideLocked     RideState = "locked"
	RideWonPending RideState = "won_pending"
	RideBurned     RideState = "burned"
	RideUnlocked   RideState = "unlocked"
	RideLost       RideState = "lost"
)

type Ride struct {
	ID           int       `json:"id"`
	CreatedAt    time.Time `json:"-"`
	PlayerID     int       `json:"player_id"`
	TeamID       int       `json:"team_id"`
	MatchID      int       `json:"match_id"`
	State        RideState `json:"state"`
	TokensLocked Decimal   `json:"token_locked"`
	BaseAtLock   Decimal   `json:"base_at_lock"`
	Acc          Decimal   `json:"-"`
	Streak       int       `json:"-"`
}

var rides []Ride

func (r *Ride) Insert(playerID, teamID, matchID int, tokensLocked, baseAtLock Decimal) (Ride, error) {
	ride := Ride{
		ID:           len(rides),
		CreatedAt:    time.Now(),
		PlayerID:     playerID,
		TeamID:       teamID,
		MatchID:      matchID,
		State:        "locked",
		TokensLocked: tokensLocked,
		BaseAtLock:   baseAtLock,
		Acc:          decimal.Zero,
		Streak:       0,
	}

	rides = append(rides, ride)

	return ride, nil
}

var transitions = map[RideState][]RideState{
	RideLocked:     {RideWonPending, RideLost},
	RideWonPending: {RideLocked, RideBurned, RideUnlocked},
}

func (r *Ride) canTransition(rideState RideState) bool {
	allowedStates := transitions[r.State]

	return slices.Contains(allowedStates, rideState)
}

var ErrInvalidTransition = errors.New("invalid state transition")

// Player decides to contiue the ride after the win
func (r *Ride) Lock(ride Ride) (Ride, error) {
	if !r.canTransition(RideLocked) {
		return Ride{}, ErrInvalidTransition
	}

	// match, err := matches.NextMatch(ride.TeamID)
	match := Match{
		ID: 1,
	}

	ride.State = RideLocked
	ride.MatchID = match.ID
	ride.Streak = ride.Streak + 1

	return ride, nil
}

// Player decides to burn after the win
func (r *Ride) Burn(ride Ride) (Ride, error) {
	if !r.canTransition(RideBurned) {
		return Ride{}, ErrInvalidTransition
	}

	ride.State = RideBurned

	return ride, nil
}

// Player decides to Unlock tokens after the win
func (r *Ride) Unlock(ride Ride) (Ride, error) {
	if !r.canTransition(RideUnlocked) {
		return Ride{}, ErrInvalidTransition
	}

	ride.State = RideUnlocked
	ride.Acc = decimal.Zero

	return ride, nil
}

// Player lost the match
func (r *Ride) Lost(ride Ride) (Ride, error) {
	if !r.canTransition(RideLost) {
		return Ride{}, ErrInvalidTransition
	}

	ride.State = RideLost
	ride.Acc = decimal.Zero

	return ride, nil
}
