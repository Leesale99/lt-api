package game

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrRecordNotFound is returned by store Get methods when no record with the
	// given ID exists.
	ErrRecordNotFound = errors.New("record not found")

	// ErrRecordInUse is returned by store Delete methods when the record is
	// still referenced by other rows (FK violation), so it must not be deleted.
	ErrRecordInUse = errors.New("record in use")

	// ErrEditConflict is returned by store update methods when the record's
	// version has changed since it was read (optimistic concurrency check),
	// meaning another request modified the row in the meantime.
	ErrEditConflict = errors.New("edit conflict")
)

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
