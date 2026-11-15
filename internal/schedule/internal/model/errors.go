package model

import "errors"

// The schedule context's own sentinel errors. The repository layer translates
// Postgres facts into these (23505 → ErrDuplicateRecord, 0 rows on a guarded
// write → ErrEditConflict, a violated reference/lifecycle gate →
// ErrRecordInUse), the services pass them through, and the handlers map them
// to the wire — the same meanings the shared persistence kernel used to
// supply, now owned by the module that raises them (phase 6).
var (
	// ErrRecordNotFound reports that a read matched no row (or that a guarded
	// write targeted an absent one).
	ErrRecordNotFound = errors.New("schedule: record not found")

	// ErrRecordInUse reports that a delete (or a lifecycle regression) is
	// refused because other rows still reference the record — an FK
	// RESTRICT, a cross-context reference the delete policy composed, or an
	// ADR-007/ADR-008 freeze gate.
	ErrRecordInUse = errors.New("schedule: record in use")

	// ErrEditConflict reports that a version-guarded write lost its CAS.
	ErrEditConflict = errors.New("schedule: edit conflict")

	// ErrDuplicateRecord reports a unique-value clash on a write (e.g. a
	// round number that already exists in the season).
	ErrDuplicateRecord = errors.New("schedule: duplicate record")

	// ErrEndedAtInFuture reports a resolve whose ended_at lies at or after the
	// current time. This is time policy, not request shape: it lives at the
	// service door (ADR-026) so every ResolveMatch caller — not only the HTTP
	// handler — passes the same rule. (It moved here from game with the
	// resolve entry point is schedule's script now.)
	ErrEndedAtInFuture = errors.New("ended_at must be in the past")
)
