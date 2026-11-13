// Package schedule owns the league calendar: teams, seasons, rounds and
// matches — their entities, validators and stores — plus the pure phase
// derivation from real-world match times. The schedule's writes are the
// season/round/match state machines; their advisory Go-side validators and
// the authoritative freeze gates (ADR-008) keep the two halves in
// lockstep.
//
// Package layout is the ADR-028 shape (Transaction Script + Layered-3):
// anemic entities + validators here, straightforward store SQL, and no
// service layer of its own yet — schedule CRUD carries no rules beyond the
// validators, so handlers drive the stores directly. The lifecycle
// exceptions (RoundStore.Open/Close) are transactions, not scripts.
//
// Phase B of the Game/Schedule split: this package was carved out of
// internal/game; game consumes schedule lookups through the store injected
// into its Service (game → schedule is the only legal import direction).
package schedule

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store composes the schedule's per-entity stores. It is the single
// dependency the API layer holds for calendar CRUD and the one game.Service
// receives for its calendar lookups (phase windows, next-match pick,
// resolve's guarded write). Generic storage primitives (sentinel errors,
// Postgres error classifiers, pagination) live in internal/store, shared
// with the other contexts.
//
// Pool rides on Store so a use-case method can own a transaction that spans
// several stores (RoundStore.Open and Close are the consumers).
type Store struct {
	Pool    *pgxpool.Pool
	Seasons SeasonStore
	Rounds  RoundStore
	Teams   TeamStore
	Matches MatchStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:    pool,
		Seasons: SeasonStore{pool},
		Rounds:  RoundStore{pool},
		Teams:   TeamStore{pool},
		Matches: MatchStore{pool},
	}
}
