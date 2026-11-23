package ledger

import (
	"context"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// Repository is the Ledger implementation behind the intent seam: the rides
// table's money facts (bonus_acc) are the only balances that exist, so its
// write SQL lives here — moved out of game's RideStore, whose state machine
// no longer touches money columns. Phase 06 swaps this type (and adds the
// accounts/journal tables) behind the unchanged interface.
type Repository struct{}

// New returns the ledger backed by the rides table's money facts.
func New() *Repository { return &Repository{} }

// LockTokens records the ride-creation lock. Nothing to write yet: the
// locked amount and its base price are born on the ride row (game's birth
// INSERT — the rides_tokens_locked_check admits no zero-token birth, so
// lock and row are one statement, not a two-step lock onto an existing
// balance). Phase 06 moves the real movement (player balance debit, token
// account credit) here.
func (r *Repository) LockTokens(_ context.Context, _ persistence.Querier, _, _, _ int, _, _ decimal.Decimal) error {
	return nil
}

// AccrueBonus adds the won ride's bonus delta to the ride's accrued bonus.
// The statement is an in-place increment (bonus_acc = bonus_acc + delta):
// the caller computed the delta from the ride's snapshot, so the increment
// composes correctly even if a read in the same tx already saw a newer
// value. 0 rows means the ride vanished mid-tx — refuse, never silently
// pass.
func (r *Repository) AccrueBonus(ctx context.Context, q persistence.Querier, rideID int, delta decimal.Decimal) error {
	tag, err := q.Exec(ctx, `
		UPDATE rides
		SET bonus_acc = bonus_acc + $2
		WHERE id = $1
	`, rideID, delta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReleaseBonus zeroes the ride's accrued bonus (unlock/lost arrivals: the
// bonus is forfeited, never paid out). Runs before the state arrival write
// — rides_money_state_coupling (000013) refuses an unlocked|lost row
// carrying acc, so the money write must precede the state write inside the
// caller's transaction. This UPDATE is the copy that reaches the row:
// domain.Ride.Unlock/Lost zero only an in-memory acc the repository never
// persists, so they document the intent and this release-before-flip order
// carries it. Change-detector: the constraint and this method are the
// load-bearing pair — release before the arrival, or the CHECK refuses it —
// and TestRideMoneyStateCoupling
// (internal/game/internal/core/service/constraints_test.go) pins the refusal
// name the two share.
// rule:R-GAME-005
func (r *Repository) ReleaseBonus(ctx context.Context, q persistence.Querier, rideID int) error {
	tag, err := q.Exec(ctx, `
		UPDATE rides
		SET bonus_acc = 0
		WHERE id = $1
	`, rideID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Burn records the ride's tokens fulfilled as TB. Nothing to write yet:
// the rides row keeps its money columns on a burned arrival (the 000013
// coupling deliberately excludes burn), and no TB balance exists. Phase 06
// moves the real movement (token account debit, TB credit) here.
func (r *Repository) Burn(_ context.Context, _ persistence.Querier, _ int, _ decimal.Decimal) error {
	return nil
}
