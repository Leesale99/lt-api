package model

import "errors"

// The identity context's sentinel errors — the vocabulary identity-internal
// control flow speaks. The repository layer translates storage facts into
// them (pgx.ErrNoRows → ErrNotFound, the users_email_key unique violation →
// ErrDuplicateEmail, a zero-row guarded UPDATE → ErrEditConflict), so no
// layer above the repository needs the shared persistence sentinels.
//
// The HTTP mapping is the handler's (and, on the authentication path, the
// facade's): ErrNotFound on a bearer token is a 401, exactly as the identity
// store's persistence.ErrRecordNotFound was before the layering.
var (
	// ErrNotFound reports an unknown user or token.
	ErrNotFound = errors.New("identity: record not found")
	// ErrEditConflict reports a guarded write that lost the version race.
	ErrEditConflict = errors.New("identity: edit conflict")
	// ErrDuplicateEmail reports a users_email_key unique violation.
	ErrDuplicateEmail = errors.New("identity: duplicate email")
	// ErrAlreadyActivated reports an activation-token request for a user who
	// is already active.
	ErrAlreadyActivated = errors.New("identity: user is already activated")
	// ErrInvalidCredentials reports a failed email/password authentication.
	// Unknown email and wrong password share it: both are the same 401, and
	// keeping them indistinguishable keeps the code path from leaking which
	// half failed.
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
)
