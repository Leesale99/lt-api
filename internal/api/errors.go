package api

// This file holds what is left of the response writers in api: the two the
// router registers by name, the game-flavored ones whose message encodes a
// rule this package owns (ride commands, idempotency), and the unique-value
// writer the player handlers share. The schedule-flavored writers (record in
// use, lifecycle freeze) moved into the schedule module with its handlers.
// The generic status writers live in httpx (httpx/errors.go); the envelope
// and its logging path are shared through httpx.WriteError, so nothing is
// duplicated.

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/shared/httpx"
)

// notFoundResponse and methodNotAllowedResponse are what routes.go assigns to
// the router's NotFound/MethodNotAllowed handlers.
func (app *Application) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	httpx.NotFound(w, r)
}

func (app *Application) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	httpx.MethodNotAllowed(w, r)
}

func (app *Application) logError(r *http.Request, err error) {
	httpx.LogError(app.Logger, r, err)
}

// writeError is the envelope writer for the domain-flavored responses below:
// the message and status are the handler's, the body shape is httpx's. A
// marshal failure can only be the payload's fault, so it is logged here.
func (app *Application) writeError(w http.ResponseWriter, r *http.Request, status int, message any) {
	err := httpx.WriteError(w, status, message)
	if err != nil {
		app.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (app *Application) duplicateRecordResponse(w http.ResponseWriter, r *http.Request) {
	message := "a record with these unique values already exists"
	app.writeError(w, r, http.StatusConflict, message)
}

// roundNotOpenResponse refuses a ride command issued on a round that is not
// open. Distinct from the schedule module's freeze refusal (temporal
// hierarchy freeze): the round exists and is valid, it just isn't accepting
// commands. Refusal happens here, before phase derivation — a closed round
// has no phase.
func (app *Application) roundNotOpenResponse(w http.ResponseWriter, r *http.Request) {
	message := "ride commands require an open round"
	app.writeError(w, r, http.StatusConflict, message)
}

// noNextMatchResponse refuses a Lock command when the team has no remaining
// match to continue into (e.g. the season has finished). The ride itself
// exists and is in a valid state — the conflict is with the team's
// schedule, not the request. Distinct from roundNotOpenResponse (the ride's
// current round refuses commands) — here the refusal is about the
// destination, not the origin.
func (app *Application) noNextMatchResponse(w http.ResponseWriter, r *http.Request) {
	message := "the ride cannot be continued because the team has no upcoming matches"
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) invalidPhaseResponse(w http.ResponseWriter, r *http.Request) {
	message := "this ride command is not allowed while the round is in current phase"
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) invalidStateTransitionResponse(w http.ResponseWriter, r *http.Request) {
	message := "this action is not allowed for a ride in current state"
	app.writeError(w, r, http.StatusConflict, message)
}

// idempotencyConflictResponse refuses a request whose Idempotency-Key was
// already used with a different request (ADR-024) — key misuse, not a retry:
// a retry re-sends the identical request and gets the stored response.
func (app *Application) idempotencyConflictResponse(w http.ResponseWriter, r *http.Request) {
	message := "this idempotency key was already used with a different request"
	app.writeError(w, r, http.StatusConflict, message)
}
