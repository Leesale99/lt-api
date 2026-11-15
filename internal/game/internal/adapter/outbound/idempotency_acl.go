package outbound

// The idempotency anti-corruption layer: the core's port.Idempotency speaks
// game's own Token/SavedResponse, the transverse idempotency package speaks
// its own. The two shapes are identical field-for-field, so this adapter is
// a pair of type conversions — but it keeps the dependency pointing outward
// (core → port, adapter → infrastructure) and the core free of the
// infrastructure's types.

import (
	"context"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/kernel/idempotency"
)

// Idempotency adapts idempotency.Repository to port.Idempotency.
type Idempotency struct {
	repo idempotency.Repository
}

var _ port.Idempotency = Idempotency{}

func NewIdempotency(repo idempotency.Repository) Idempotency {
	return Idempotency{repo: repo}
}

// Claim tries to claim the key inside tx (see port.Idempotency).
func (a Idempotency) Claim(ctx context.Context, tx pgx.Tx, token port.Token) (bool, port.SavedResponse, error) {
	claimed, res, err := a.repo.Claim(ctx, tx, idempotency.IdempotencyToken(token))
	return claimed, port.SavedResponse(res), err
}

// Save stores the executed command's answer inside tx.
func (a Idempotency) Save(ctx context.Context, tx pgx.Tx, token port.Token, res port.SavedResponse) error {
	return a.repo.Save(ctx, tx, idempotency.IdempotencyToken(token), idempotency.IdempotentResponse(res))
}
