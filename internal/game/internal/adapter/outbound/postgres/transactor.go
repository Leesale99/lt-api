package postgres

// The unit of work: the ride commands and the settlement handlers each own
// one transaction, opened here on the game context's own pool, and (for the
// settlement handlers) serialized per round by the advisory lock below.
//
// The advisory-lock SQL is a persistence detail, so it lives in the adapter
// and the core calls the seam: the two settlement transactions of one round
// run back to back, never beside each other (pg_advisory_xact_lock
// namespace class game → round id). Without it the RoundCompleted settlement
// could lose the locked→won_pending CAS to a MatchResultRecorded settlement
// running beside it, roll back WITH its idempotency claim (an unprocessed
// event), and be dropped by the synchronous bus.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
)

// Transactor is the Postgres implementation of port.Transactor.
type Transactor struct {
	pool *pgxpool.Pool
}

var _ port.Transactor = Transactor{}

func NewTransactor(pool *pgxpool.Pool) Transactor {
	return Transactor{pool: pool}
}

// Begin opens a transaction on the game pool.
func (t Transactor) Begin(ctx context.Context) (pgx.Tx, error) {
	return t.pool.Begin(ctx)
}

// The settlement handlers' serialization: a transaction-scoped advisory lock
// per settled round, taken inside the handler's own transaction. Class id
// namespaces game's advisory-lock keyspace (the only user — the second key is
// the round id); the value itself is arbitrary.
const (
	settlementLockClass = 1
	settlementLockSQL   = `SELECT pg_advisory_xact_lock($1, $2)`
)

// LockSettlementRound takes the per-round advisory lock inside tx.
// rule:R-GAME-023
func (t Transactor) LockSettlementRound(ctx context.Context, tx pgx.Tx, roundID int) error {
	_, err := tx.Exec(ctx, settlementLockSQL, settlementLockClass, roundID)
	return err
}
