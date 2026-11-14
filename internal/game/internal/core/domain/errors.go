package domain

// The ride domain's sentinel errors — the vocabulary every layer above the
// domain (ports, adapters, transport) speaks. They are game-owned on
// purpose: the anti-corruption layer at adapter/outbound translates the
// calendar's and Postgres's own errors into these, so no storage or
// calendar or storage error type ever crosses into the core.
//
// The storage-shaped ones (ErrNotFound, ErrEditConflict, ErrRecordInUse,
// ErrDuplicateRecord) mirror the persistence kernel's meanings one-for-one;
// the ride rules (ErrInvalidTransition, ErrRoundNotOpen, ErrInvalidRoundPhase)
// are the state machine's own. ErrNoNextMatch is the continuation pick's
// refusal, raised when the calendar has no destination to offer.

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound reports that the addressed record does not exist — the
	// absent row, the unknown parent reference.
	ErrNotFound = errors.New("record not found")

	// ErrEditConflict reports that a version-guarded write lost its CAS:
	// the row changed since it was read.
	ErrEditConflict = errors.New("edit conflict")

	// ErrRecordInUse reports that a delete is refused because other rows
	// still reference the record.
	ErrRecordInUse = errors.New("record in use")

	// ErrDuplicateRecord reports a unique-value clash on a write.
	ErrDuplicateRecord = errors.New("duplicate record")

	// ErrForbidden reports that the authenticated principal has no authority
	// over the operation (authorization is a use-case rule, not a transport
	// one).
	ErrForbidden = errors.New("forbidden")

	// ErrInvalidTransition rejects a command invalid from the ride's current
	// state: terminal states accept nothing, and re-issuing a command whose
	// state has already moved on.
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrRoundNotOpen refuses a ride command issued on a round that is not
	// open. Distinct from the calendar's lifecycle freeze: the round exists
	// and is valid, it just is not accepting commands.
	ErrRoundNotOpen = errors.New("round is not open")

	// ErrNoNextMatch refuses a Lock command when the team has no remaining
	// match to continue into (e.g. the season has finished).
	ErrNoNextMatch = errors.New("team has no upcoming match")

	// ErrInvalidRoundPhase rejects a ride command issued in the wrong round
	// phase (e.g. Burn during MatchPhase). Distinct from ErrInvalidTransition
	// so handlers can map the two to different client-facing errors. Which
	// phase a command is legal in is the ride domain's rule, so the error
	// lives here; the phase itself arrives through the calendar port.
	ErrInvalidRoundPhase = errors.New("command not allowed in this round phase")
)

// ValidationError carries the field-level messages a use case's validation
// produced (validator.Validator's map shape). It is the domain's 422
// contract: the transport writes the map verbatim, so a validation refusal
// looks the same whichever layer raised it.
type ValidationError struct {
	FieldErrors map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.FieldErrors)
}

// NewValidationError builds a ValidationError over a copy of the map.
func NewValidationError(fieldErrors map[string]string) *ValidationError {
	return &ValidationError{FieldErrors: fieldErrors}
}

// AsValidationError reports whether err is (or wraps) a *ValidationError and
// returns it.
func AsValidationError(err error) (*ValidationError, bool) {
	var verr *ValidationError
	if errors.As(err, &verr) {
		return verr, true
	}
	return nil, false
}
