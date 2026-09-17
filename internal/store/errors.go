// Package store holds the storage-layer primitives shared by every domain
// package (game, identity): sentinel errors that the API layer translates
// into HTTP responses, Postgres error-code classifiers, and the pagination
// primitives that shape list queries. It must stay free of domain types so
// domains never import each other through it.
package store

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrRecordNotFound is returned by store Get methods when no record with
	// the given ID exists.
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

// Code reports whether err is a PostgreSQL error carrying one of the given
// SQLSTATE codes (pgerrcode inlined to avoid the extra dependency).
func Code(err error, codes ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return slices.Contains(codes, pgErr.Code)
}

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (23505). Without constraint names it matches any such violation;
// with names it matches only those constraints, so a store can collapse a
// specific uniqueness clash into a specific error (e.g. a duplicate email vs
// ErrDuplicateRecord) while leaving other unique violations unhandled.
func IsUniqueViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return len(constraints) == 0 || slices.Contains(constraints, pgErr.ConstraintName)
}

// IsFKViolation reports whether err is a PostgreSQL foreign-key violation
// (23503). It lets stores collapse a violated reference into
// ErrRecordNotFound so handlers can translate it into a validation error
// instead of a 500.
func IsFKViolation(err error) bool { return Code(err, "23503") }

// IsTriggerViolation reports whether err is a PostgreSQL raised exception
// (P0001) — the ADR-007/ADR-008 gate pattern: BEFORE triggers RAISE
// EXCEPTION with ERRCODE P0001 to refuse lifecycle-illegal writes, and the
// store maps it to ErrRecordInUse so handlers answer 409.
func IsTriggerViolation(err error) bool { return Code(err, "P0001") }
