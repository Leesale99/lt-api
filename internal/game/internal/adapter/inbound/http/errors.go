package http

// The game-flavored response writers: the messages encode rules this context
// owns (ride state machine, round gates, idempotency, unique values,
// authorization). The generic status writers live in httpx (httpx/errors.go);
// the envelope and its logging path are shared through httpx.WriteError, so
// nothing is duplicated.

import (
	"context"
	"errors"
	"net/http"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/idempotency"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
)

// gameErrorResponse maps game errors to HTTP responses. It returns true
// (having written a response) for every error it sees; the client-gone case
// writes nothing at all.
func (h *Handler) gameErrorResponse(w http.ResponseWriter, r *http.Request, err error) bool {
	if verr, ok := domain.AsValidationError(err); ok {
		httpx.FailedValidation(w, r, verr.FieldErrors)
		return true
	}

	switch {
	case errors.Is(err, context.Canceled):
		// Client gone; response would be discarded anyway.
	case errors.Is(err, domain.ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, domain.ErrEditConflict):
		// Repository-level optimistic-concurrency refusals (version guards,
		// the settlement classification) — 409, same as the edit handlers
		// that map it inline.
		httpx.EditConflict(w, r)
	case errors.Is(err, domain.ErrForbidden):
		httpx.Forbidden(w, r)
	case errors.Is(err, domain.ErrRoundNotOpen):
		h.roundNotOpenResponse(w, r)
	case errors.Is(err, domain.ErrNoNextMatch):
		h.noNextMatchResponse(w, r)
	case errors.Is(err, domain.ErrInvalidRoundPhase):
		h.invalidPhaseResponse(w, r)
	case errors.Is(err, domain.ErrInvalidTransition):
		h.invalidStateTransitionResponse(w, r)
	case errors.Is(err, domain.ErrDuplicateRecord):
		h.duplicateRecordResponse(w, r)
	case errors.Is(err, domain.ErrRecordInUse):
		h.recordInUseResponse(w, r)
	case errors.Is(err, idempotency.ErrIdempotencyConflict):
		h.idempotencyConflictResponse(w, r)
	default:
		httpx.ServerError(w, r, h.log, err)
	}
	return true
}

// writeError is the envelope writer for the domain-flavored responses below:
// the message and status are the handler's, the body shape is httpx's. A
// marshal failure can only be the payload's fault, so it is logged here.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message any) {
	err := httpx.WriteError(w, status, message)
	if err != nil {
		httpx.LogError(h.log, r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (h *Handler) duplicateRecordResponse(w http.ResponseWriter, r *http.Request) {
	message := "a record with these unique values already exists"
	h.writeError(w, r, http.StatusConflict, message)
}

func (h *Handler) recordInUseResponse(w http.ResponseWriter, r *http.Request) {
	message := "the record is referenced by other records and cannot be deleted"
	h.writeError(w, r, http.StatusConflict, message)
}

// roundNotOpenResponse refuses a ride command issued on a round that is not
// open. Distinct from the calendar's freeze refusal (temporal hierarchy
// freeze): the round exists and is valid, it just isn't accepting commands.
// Refusal happens before phase derivation — a closed round has no phase.
func (h *Handler) roundNotOpenResponse(w http.ResponseWriter, r *http.Request) {
	message := "ride commands require an open round"
	h.writeError(w, r, http.StatusConflict, message)
}

// noNextMatchResponse refuses a Lock command when the team has no remaining
// match to continue into (e.g. the season has finished). The ride itself
// exists and is in a valid state — the conflict is with the team's schedule,
// not the request.
func (h *Handler) noNextMatchResponse(w http.ResponseWriter, r *http.Request) {
	message := "the ride cannot be continued because the team has no upcoming matches"
	h.writeError(w, r, http.StatusConflict, message)
}

func (h *Handler) invalidPhaseResponse(w http.ResponseWriter, r *http.Request) {
	message := "this ride command is not allowed while the round is in current phase"
	h.writeError(w, r, http.StatusConflict, message)
}

func (h *Handler) invalidStateTransitionResponse(w http.ResponseWriter, r *http.Request) {
	message := "this action is not allowed for a ride in current state"
	h.writeError(w, r, http.StatusConflict, message)
}

// idempotencyConflictResponse refuses a request whose Idempotency-Key was
// already used with a different request (ADR-024) — key misuse, not a retry:
// a retry re-sends the identical request and gets the stored response.
func (h *Handler) idempotencyConflictResponse(w http.ResponseWriter, r *http.Request) {
	message := "this idempotency key was already used with a different request"
	h.writeError(w, r, http.StatusConflict, message)
}
