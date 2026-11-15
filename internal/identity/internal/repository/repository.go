// Package repository holds the identity context's storage layer: one
// repository per table (users, user_tokens, permissions). Straightforward
// storage SQL lives here — no rules beyond what the statements and their
// error classification encode.
//
// Error translation is this package's contract: pgx/persistence facts become
// the model's sentinels (model.ErrNotFound, model.ErrEditConflict,
// model.ErrDuplicateEmail), so the layers above speak identity's vocabulary
// instead of the shared persistence one.
package repository
