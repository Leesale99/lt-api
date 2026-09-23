package game

import (
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

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
	RoundID      int             `json:"round_id"`
	SeasonID     int             `json:"season_id"`
	State        RideState       `json:"state"`
	TokensLocked decimal.Decimal `json:"token_locked"`
	BaseAtLock   decimal.Decimal `json:"base_at_lock"`
	Acc          decimal.Decimal `json:"-"`
	Streak       int             `json:"-"`
}

var rides []Ride

func (r *Ride) Insert(playerID, teamID, matchID, roundID, seasonID int, tokensLocked, baseAtLock decimal.Decimal) (Ride, error) {
	ride := Ride{
		ID:           len(rides),
		CreatedAt:    time.Now(),
		PlayerID:     playerID,
		TeamID:       teamID,
		MatchID:      matchID,
		RoundID:      roundID,
		SeasonID:     seasonID,
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

func (r *Ride) canTransition(nextRideState RideState) bool {
	allowedStates := transitions[r.State]

	return slices.Contains(allowedStates, nextRideState)
}

var ErrInvalidTransition = errors.New("invalid state transition")

// matches, err := matches.GetAll(ride.RoundID)
var matches = []Match{
	{
		ID:       1,
		StartsAt: time.Now().Add(24 * time.Hour),
	},
	{
		ID:       2,
		StartsAt: time.Now().Add(72 * time.Hour),
	},
}

// MatchPhase: Match won, ride transition to won_pending
func (r *Ride) WonPending(ride Ride) (Ride, error) {
	if !r.canTransition(RideWonPending) {
		return Ride{}, ErrInvalidTransition
	}

	roundPhase := getRoundPhase(time.Now(), matches[0].StartsAt, matches[len(matches)-1].StartsAt)
	if roundPhase != MatchPhase {
		return Ride{}, ErrorInvalidRoundPhase
	}

	ride.State = RideWonPending

	return ride, nil
}

// MatchPhase: Match lost, ride transition to lost
func (r *Ride) Lost(ride Ride) (Ride, error) {
	if !r.canTransition(RideLost) {
		return Ride{}, ErrInvalidTransition
	}

	roundPhase := getRoundPhase(time.Now(), matches[0].StartsAt, matches[len(matches)-1].StartsAt)
	if roundPhase != MatchPhase {
		return Ride{}, ErrorInvalidRoundPhase
	}

	ride.State = RideLost
	ride.Acc = decimal.Zero

	return ride, nil
}

// DecisionPhase: Player decides to contiue the ride after the win
func (r *Ride) Lock(ride Ride) (Ride, error) {
	if !r.canTransition(RideLocked) {
		return Ride{}, ErrInvalidTransition
	}

	roundPhase := getRoundPhase(time.Now(), matches[0].StartsAt, matches[len(matches)-1].StartsAt)
	if roundPhase != DecisionPhase {
		return Ride{}, ErrorInvalidRoundPhase
	}

	// match, err := matches.NextMatch(ride.TeamID)
	match := matches[0]

	ride.State = RideLocked
	ride.MatchID = match.ID
	ride.Streak = ride.Streak + 1

	return ride, nil
}

// DecisionPhase: Player decides to burn after the win
func (r *Ride) Burn(ride Ride) (Ride, error) {
	if !r.canTransition(RideBurned) {
		return Ride{}, ErrInvalidTransition
	}

	roundPhase := getRoundPhase(time.Now(), matches[0].StartsAt, matches[len(matches)-1].StartsAt)
	if roundPhase != DecisionPhase {
		return Ride{}, ErrorInvalidRoundPhase
	}

	ride.State = RideBurned

	return ride, nil
}

// DecisionPhase: Player decides to Unlock tokens after the win
func (r *Ride) Unlock(ride Ride) (Ride, error) {
	if !r.canTransition(RideUnlocked) {
		return Ride{}, ErrInvalidTransition
	}

	roundPhase := getRoundPhase(time.Now(), matches[0].StartsAt, matches[len(matches)-1].StartsAt)
	if roundPhase != DecisionPhase {
		return Ride{}, ErrorInvalidRoundPhase
	}

	ride.State = RideUnlocked
	ride.Acc = decimal.Zero

	return ride, nil
}
