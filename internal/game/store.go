package game

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
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

	// ErrDuplicateRecord is returned by store write methods when the insert or
	// update violates a unique constraint (23505), e.g. a round number that
	// already exists in the season.
	ErrDuplicateRecord = errors.New("duplicate record")
)

// fkViolation reports whether err is a PostgreSQL foreign-key violation
// (23503; pgerrcode inlined to avoid a dependency, matching teams.go).
// It lets the store collapse a violated reference into ErrRecordNotFound so
// handlers can translate it into a validation error instead of a 500.
func fkViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// uniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (23505; pgerrcode inlined to avoid a dependency, matching
// fkViolation). It lets stores collapse a uniqueness clash into
// ErrDuplicateRecord so handlers can translate it into a 409.
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

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
