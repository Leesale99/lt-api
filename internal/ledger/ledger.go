// Package ledger is the money-movement authority (phase-05/06): game sends
// intents, the ledger owns every mutation of a balance. Game code never
// UPDATEs money columns directly and never sees how balances are stored —
// Phase 06 swaps this implementation to real accounts/journal/balances
// behind the same interface (the phase-05 example shape), with no game
// change.
//
// Current state of the implementation: there are no balance tables yet, so
// the concrete Store (store.go) persists the money facts the rides table
// already carries — bonus_acc — inside the CALLER's transaction (game and
// ledger share one Postgres tx; no async). LockTokens and Burn are the
// intent seams whose real effect (debit a player balance, credit a token
// account, move locked tokens to TB) arrives with the Phase 06 tables; the
// seams are wired today so the swap is an implementation change only.
//
// Every method takes the caller's querier (pool or transaction) and runs
// its statements on it — the ledger is in-tx, never a separate unit of
// work.
package ledger

import (
	"context"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/store"
)

// Ledger is the money seam game consumes. Intent-style names: the caller
// states what should happen to the money, not how the rows change.
type Ledger interface {
	// LockTokens records that a player locked tokens on a ride (ride
	// creation). Phase 06 will debit the player's currency balance and
	// credit a per-team token account; today the intent is verified and
	// carried by the ride row's tokens_locked, which the birth INSERT
	// writes (the rides CHECK admits no zero-token birth, so the lock and
	// the row are one statement — see store.go).
	LockTokens(ctx context.Context, q store.Querier, playerID, teamID, rideID int, tokens, base decimal.Decimal) error

	// AccrueBonus adds a won ride's accrued bonus. game owns the bonus
	// formula (the streak-rate chain, ADR-021) and passes the delta; the
	// ledger owns the balance write and the rounding/exactness guarantees.
	AccrueBonus(ctx context.Context, q store.Querier, rideID int, delta decimal.Decimal) error

	// ReleaseBonus forfeits a ride's accrued bonus: zero on the unlock and
	// lost arrivals (the money-state rule ride.Unlock/ride.Lost obey, and
	// the rides_money_state_coupling CHECK enforces at the row).
	ReleaseBonus(ctx context.Context, q store.Querier, rideID int) error

	// Burn records that a ride's locked tokens are fulfilled as TB — the
	// player burn command and the season-end force-burn of undecided rides.
	// Phase 06 will debit the token account / credit the TB side; today
	// there is nothing to write (the rides row keeps its money columns on a
	// burned arrival — the 000013 CHECK deliberately does not couple burn).
	Burn(ctx context.Context, q store.Querier, rideID int, tokens decimal.Decimal) error
}
