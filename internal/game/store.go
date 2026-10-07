package game

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store composes the per-entity stores. It is the single dependency the API
// layer holds; swapping to Postgres changes the guts of each store, not the
// shape of Store. Generic storage primitives (sentinel errors, Postgres
// error classifiers, pagination) live in internal/store, shared with the
// identity package.
//
// Pool rides on Store so a use-case method can own a transaction that spans
// several stores (the idempotent lock command is the first consumer).
type Store struct {
	Pool        *pgxpool.Pool
	Seasons     SeasonStore
	Rounds      RoundStore
	Teams       TeamStore
	Matches     MatchStore
	Players     PlayerStore
	Rides       RideStore
	Idempotency IdempotencyStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:        pool,
		Seasons:     SeasonStore{pool},
		Rounds:      RoundStore{pool},
		Teams:       TeamStore{pool},
		Matches:     MatchStore{pool},
		Players:     PlayerStore{pool},
		Rides:       RideStore{pool},
		Idempotency: IdempotencyStore{},
	}
}
