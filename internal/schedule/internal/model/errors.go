package model

import "errors"

// ErrEndedAtInFuture reports a resolve whose ended_at lies at or after the
// current time. This is time policy, not request shape: it lives at the
// service door (ADR-026) so every ResolveMatch caller — not only the HTTP
// handler — passes the same rule. (It moved here from game with the
// resolve entry point is schedule's script now.)
//
// The schedule context's own sentinel errors live in this package. For now
// it holds only the one the service door raises: the storage layer still
// speaks internal/shared/persistence's vocabulary (ErrRecordNotFound,
// ErrEditConflict, ErrRecordInUse, ErrDuplicateRecord), because the
// game-facing surface must keep returning exactly those until the error
// contract flips in phase 4.
var ErrEndedAtInFuture = errors.New("ended_at must be in the past")
