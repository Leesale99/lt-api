package port

// The money seam. The ledger is the authority on every money mutation: game
// states intents, the ledger owns the writes, and both run inside the
// caller's transaction (game and ledger share one Postgres tx; no async).
//
// The interface is consumer-side and deliberately structural: the concrete
// internal/ledger.Ledger satisfies it as-is, so the composition root hands
// the ledger facade over without an adapter (and game never names the
// ledger's Repository type). Phase 06 swaps the ledger's implementation
// behind that interface with no game change.

import (
	"context"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// Ledger is the money seam game consumes. Intent-style names: the caller
// states what should happen to the money, not how the rows change.
type Ledger interface {
	// LockTokens records that a player locked tokens on a ride (ride
	// creation). The ride row is born carrying the locked tokens (the birth
	// INSERT writes them); the intent is in the flow so the Phase 06
	// balance swap changes no call shape.
	LockTokens(ctx context.Context, q persistence.Querier, playerID, teamID, rideID int, tokens, base decimal.Decimal) error

	// AccrueBonus adds a won ride's accrued bonus. game owns the bonus
	// formula (the streak-rate chain, ADR-021) and passes the delta; the
	// ledger owns the balance write and its rounding guarantees.
	AccrueBonus(ctx context.Context, q persistence.Querier, rideID int, delta decimal.Decimal) error

	// ReleaseBonus forfeits a ride's accrued bonus: zero on the unlock and
	// lost arrivals (the money-state rule the domain obeys, enforced at the
	// row by rides_money_state_coupling).
	ReleaseBonus(ctx context.Context, q persistence.Querier, rideID int) error

	// Burn records that a ride's locked tokens are fulfilled as TB — the
	// player burn command and the season-end force-burn of undecided rides.
	Burn(ctx context.Context, q persistence.Querier, rideID int, tokens decimal.Decimal) error
}
