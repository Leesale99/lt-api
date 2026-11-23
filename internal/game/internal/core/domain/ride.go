// Package domain holds the ride gameplay's entities, validators and
// vocabulary — the transport-free, storage-free core of the game module
// (hexagonal shape: everything the ride rules need and nothing they do not).
//
// It imports the standard library, the shared validator and the decimal
// value type only. No calendar, no SQL, no event bus: the ride's state
// machine speaks game's own Phase, the repositories translate rows into
// these values, and the anti-corruption layer at adapter/outbound translates
// the calendar's facts into them.
package domain

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

type RideState string

const (
	RideLocked     RideState = "locked"
	RideWonPending RideState = "won_pending"
	RideBurned     RideState = "burned"
	RideUnlocked   RideState = "unlocked"
	RideLost       RideState = "lost"
)

// Ride is one player's token commitment on one match, carried along its
// state machine. The money columns (TokensLocked, BaseAtLock, Acc) are the
// ledger's facts; the domain moves state, continuation and streak, and the
// bonus arithmetic below (ADR-021) decides what a win accrues.
type Ride struct {
	ID           int
	CreatedAt    time.Time
	PlayerID     int
	TeamID       int
	MatchID      int
	State        RideState
	TokensLocked decimal.Decimal
	BaseAtLock   decimal.Decimal
	Acc          decimal.Decimal
	Streak       int
	Version      int
}

// rule:R-GAME-004
// rule:R-GAME-020
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

// rule:R-GAME-002
var transitions = map[RideState]map[Phase][]RideState{
	RideLocked: {
		MatchPhase: {RideWonPending, RideLost},
	},
	RideWonPending: {
		DecisionPhase: {RideLocked, RideBurned, RideUnlocked},
	},
}

// rule:R-GAME-002
// rule:R-GAME-003
func canTransition(state RideState, phase Phase, next RideState) bool {
	allowedStates := transitions[state][phase]

	return slices.Contains(allowedStates, next)
}

// The application layer owns the clock: it reads the round's phase from the
// calendar port at the instant its injected clock reports, and passes the
// derived phase into each command, e.g.
//
//	phase, _ := port.PhaseForMatch(ctx, ride.MatchID, s.now())
//	ride.Burn(phase)

// Create locks the ride on a chosen match. Phase: ActionPhase; call when: Player decides to lock tokens on a chosen match.
// The creation command is the single enforcement point for the ADR-019
// initial-state contract: it overwrites state, acc and streak, so an
// invalid initial state cannot be produced through normal domain
// operations — whatever the caller supplied is discarded. Persistence is
// the repository's job.
// rule:R-GAME-001
// rule:R-GAME-007
func (r *Ride) Create(phase Phase) error {
	if phase != ActionPhase {
		return ErrInvalidRoundPhase
	}

	r.State = RideLocked
	r.Acc = decimal.Zero
	r.Streak = 0

	return nil
}

// WonPending records a won match. Phase: MatchPhase; call when: Match won; Ride transitions to won_pending
// rule:R-GAME-007
func (r *Ride) WonPending(phase Phase, odds decimal.Decimal) error {
	if phase != MatchPhase {
		return ErrInvalidRoundPhase
	}
	if !canTransition(r.State, phase, RideWonPending) {
		return ErrInvalidTransition
	}

	r.State = RideWonPending
	r.Acc = CalculateBonus(odds, r.TokensLocked, r.Acc, r.Streak)

	return nil
}

// Lost records a lost match. Phase: MatchPhase; call when: Match lost; Ride transitions to lost.
// The Acc zero is intent, not persistence: the ledger's ReleaseBonus zeroes
// the row before the arrival (rides_money_state_coupling, 000013, refuses
// an unlocked|lost row that still carries acc).
// rule:R-GAME-005
// rule:R-GAME-007
func (r *Ride) Lost(phase Phase) error {
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
// rule:R-GAME-007
// rule:R-GAME-025
func (r *Ride) Lock(phase Phase, nextMatchID int) error {
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
// rule:R-GAME-007
func (r *Ride) Burn(phase Phase) error {
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
// bonus is cleared. Phase: DecisionPhase; call when: Player decides to Unlock tokens after the win.
// The Acc zero is intent, not persistence: the ledger's ReleaseBonus zeroes
// the row before the arrival (rides_money_state_coupling, 000013, refuses
// an unlocked|lost row that still carries acc).
// rule:R-GAME-005
// rule:R-GAME-007
func (r *Ride) Unlock(phase Phase) error {
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

// BonusDelta is the accrued amount of one won match: the increment the
// ledger's AccrueBonus applies (the settlement handler's input). Kept
// separate from CalculateBonus so the accrual total and its delta share one
// formula.
// rule:R-GAME-006
func BonusDelta(matchOdds, tokensLocked decimal.Decimal, streak int) decimal.Decimal {
	s := decimal.NewFromInt(int64(streak))
	one := decimal.NewFromInt(1)

	return tokensLocked.Mul(matchOdds.Sub(one).Mul(s.Mul(streakRate).Add(one)))
}

// CalculateBonus adds one match's bonus delta to the ride's accumulated
// bonus: tokensLocked × (odds − 1) × (1 + 0.20×streak), on top of acc.
// rule:R-GAME-006
func CalculateBonus(matchOdds, tokensLocked, acc decimal.Decimal, streak int) decimal.Decimal {
	return acc.Add(BonusDelta(matchOdds, tokensLocked, streak))
}
