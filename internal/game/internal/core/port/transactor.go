package port

// The unit-of-work seam. The ride commands and the settlement handlers each
// own one transaction that spans the idempotency claim, the calendar's guard
// locks, the ride writes and the ledger's money intents — the Transactor
// opens it, the caller commits it.
//
// The port names pgx.Tx (the documented pragmatic exception): the
// repositories' *Tx methods take the same handle, so the transaction type is
// the boundary's shared currency. Everything above this line still speaks
// game's values and sentinels.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Transactor opens the commands' transactions and owns the
// transaction-scoped serialization primitive the settlement handlers take.
type Transactor interface {
	// Begin opens a transaction on the context's own pool.
	Begin(ctx context.Context) (pgx.Tx, error)

	// LockSettlementRound takes the per-round advisory lock inside tx: the
	// two settlement handlers of one round run back to back, never beside
	// each other (see the settlement handler's package notes).
	LockSettlementRound(ctx context.Context, tx pgx.Tx, roundID int) error
}
