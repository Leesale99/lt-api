// Package persistence holds the storage-layer primitives shared by every domain
// package (schedule, game, identity): the Postgres error-code classifiers that
// let a repository translate database facts into ITS module's sentinels, and
// the pagination primitives that shape list queries. It must stay free of
// domain types so domains never import each other through it.
//
// It carries no sentinel errors: record-level vocabulary (not found, in use,
// edit conflict, duplicate) belongs to the module that raises it, declared in
// its model/errors.go (phase 6).
package persistence

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
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
// specific uniqueness clash into a specific sentinel (e.g. a duplicate email
// vs a duplicate round number) while leaving other unique violations
// unhandled.
func IsUniqueViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return len(constraints) == 0 || slices.Contains(constraints, pgErr.ConstraintName)
}

// IsCheckViolation reports whether err is a PostgreSQL check-constraint
// violation (23514). Same constraint-name pattern as IsUniqueViolation:
// without names it matches any check violation, with names only those
// constraints — declarative gates refuse with the constraint's name
// instead of a P0001 message, so a store maps one specific rule by name
// while leaving the table's other CHECKs falling through unhandled.
func IsCheckViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		return false
	}
	return len(constraints) == 0 || slices.Contains(constraints, pgErr.ConstraintName)
}

// IsFKViolation reports whether err is a PostgreSQL foreign-key violation
// (23503). It lets stores collapse a violated reference into their module's
// not-found sentinel so handlers can translate it into a validation error
// instead of a 500.
func IsFKViolation(err error) bool { return Code(err, "23503") }

// IsDeleteRestricted reports whether err is a PostgreSQL violation meaning the
// row is still referenced and must not be deleted: 23001 (restrict_violation)
// for ON DELETE RESTRICT, or 23503 (foreign_key_violation) for the NO ACTION
// that Postgres raises for deferred/unenforced-at-delete FKs. Call sites map
// it to their module's record-in-use sentinel.
func IsDeleteRestricted(err error) bool { return Code(err, "23001", "23503") }

// IsTriggerViolation reports whether err is a PostgreSQL raised exception
// (P0001) — the ADR-007/ADR-008 gate pattern: BEFORE triggers RAISE
// EXCEPTION with ERRCODE P0001 to refuse lifecycle-illegal writes, and the
// store maps it to its module's record-in-use sentinel so handlers answer
// 409.
func IsTriggerViolation(err error) bool { return Code(err, "P0001") }
