// Package game owns the ride gameplay domain: the ride state machines and
// their commands, the persistence of rides and players, and the MatchResolve
// orchestration (Phase B of the Game/Schedule split — calendar lookups come
// from the schedule store injected into Service; MatchResolve still shares
// one transaction across both stores until the Phase C event seam).
package game

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/idempotency"
)

// Store composes the per-entity stores of the game context. Generic storage
// primitives (sentinel errors, Postgres error classifiers, pagination) live
// in internal/store, shared with the other contexts; the idempotency engine
// lives in internal/idempotency (transverse infra, hoisted from this
// package in Phase B).
//
// Pool rides on Store so a use-case method can own a transaction that spans
// several stores (the idempotent ride commands are the consumers).
type Store struct {
	Pool        *pgxpool.Pool
	Players     PlayerStore
	Rides       RideStore
	Idempotency idempotency.IdempotencyStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:        pool,
		Players:     PlayerStore{pool},
		Rides:       RideStore{pool},
		Idempotency: idempotency.IdempotencyStore{},
	}
}
