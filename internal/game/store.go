package game

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRecordNotFound is returned by store Get methods when no record with the
// given ID exists.
var ErrRecordNotFound = errors.New("record not found")

// Store composes the per-entity stores. It is the single dependency the API
// layer holds; swapping to Postgres changes the guts of each store, not the
// shape of Store.
type Store struct {
	Seasons SeasonStore
	Rounds  RoundStore
	Teams   TeamStore
	Matches MatchStore
	Players PlayerStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Seasons: SeasonStore{pool},
		Rounds:  RoundStore{pool},
		Teams:   TeamStore{pool},
		Matches: MatchStore{pool},
		Players: PlayerStore{pool},
	}
}
