package game

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
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

// pgCode reports whether err is a PostgreSQL error with the given SQLSTATE
// code (pgerrcode inlined to avoid the extra dependency).
func pgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// fkViolation reports whether err is a PostgreSQL foreign-key violation
// (23503). It lets the store collapse a violated reference into
// ErrRecordNotFound so handlers can translate it into a validation error
// instead of a 500.
func fkViolation(err error) bool { return pgCode(err, "23503") }

// uniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (23505). It lets stores collapse a uniqueness clash into
// ErrDuplicateRecord so handlers can translate it into a 409.
func uniqueViolation(err error) bool { return pgCode(err, "23505") }

// triggerViolation reports whether err is a PostgreSQL raised exception
// (P0001) — the ADR-007/ADR-008 gate pattern: BEFORE triggers RAISE
// EXCEPTION with ERRCODE P0001 to refuse lifecycle-illegal writes, and the
// store maps it to ErrRecordInUse so handlers answer 409.
func triggerViolation(err error) bool { return pgCode(err, "P0001") }

// dbQuerier is the subset of *pgxpool.Pool and pgx.Tx that store helpers
// use, so one write path can run either on the pool directly or inside a
// transaction (RoundStore.Open's coupled season flip is the first consumer).
type dbQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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
