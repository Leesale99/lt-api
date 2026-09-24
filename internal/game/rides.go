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
	State        RideState       `json:"state"`
	TokensLocked decimal.Decimal `json:"token_locked"`
	BaseAtLock   decimal.Decimal `json:"base_at_lock"`
	Acc          decimal.Decimal `json:"-"`
	Streak       int             `json:"-"`
	Version      int             `json:"Version"`
}

var rides []Ride

func (r *Ride) Insert(playerID, teamID, matchID, roundID, seasonID int, tokensLocked, baseAtLock decimal.Decimal) (Ride, error) {
	ride := Ride{
		ID:           len(rides),
		CreatedAt:    time.Now(),
		PlayerID:     playerID,
		TeamID:       teamID,
		MatchID:      matchID,
		State:        RideLocked,
		TokensLocked: tokensLocked,
		BaseAtLock:   baseAtLock,
		Acc:          decimal.Zero,
		Streak:       0,
		Version:      1,
	}

	rides = append(rides, ride)

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

var ErrInvalidTransition = errors.New("invalid state transition")

// matches, err := matches.GetAll(ride.RoundID)
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

// The service layer owns the clock: it fetches the round's first/last match
// start times and passes the derived phase into each command, e.g.
//
//	phase := Phase(time.Now(), firstMatchAt, lastMatchAt)
//	ride.Burn(phase)

// Phase: MatchPhase; Call when: Match won; Ride transitions to won_pending
func (r *Ride) WonPending(phase RoundPhase) error {
	if !canTransition(r.State, phase, RideWonPending) {
		return ErrInvalidTransition
	}

	matchOdds := decimal.NewFromFloat(matches[0].Odds.Home)

	r.State = RideWonPending
	r.Acc = calculateBonus(matchOdds, r.TokensLocked, r.Acc, r.Streak)

	return nil
}

// Phase: MatchPhase; Call when: Match lost; Ride transitions to lost
func (r *Ride) Lost(phase RoundPhase) error {
	if !canTransition(r.State, phase, RideLost) {
		return ErrInvalidTransition
	}

	r.State = RideLost
	r.Acc = decimal.Zero

	return nil
}

// Phase: DecisionPhase; Call when: Player decides to contiue the ride after the win
func (r *Ride) Lock(phase RoundPhase) error {
	if !canTransition(r.State, phase, RideLocked) {
		return ErrInvalidTransition
	}

	// match, err := matches.NextMatch(ride.TeamID)
	nextMatch := matches[0]

	r.State = RideLocked
	r.MatchID = nextMatch.ID
	r.Streak = r.Streak + 1

	return nil
}

// Phase: DecisionPhase; Call when: Player decides to burn after the win
func (r *Ride) Burn(phase RoundPhase) error {
	if !canTransition(r.State, phase, RideBurned) {
		return ErrInvalidTransition
	}

	r.State = RideBurned

	return nil
}

// Phase: DecisionPhase; Call when: Player decides to Unlock tokens after the win
func (r *Ride) Unlock(phase RoundPhase) error {
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
