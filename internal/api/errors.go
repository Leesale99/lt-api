package api

import (
	"fmt"
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
)

func (app *Application) logError(r *http.Request, err error) {
	var (
		method = r.Method
		uri    = r.URL.RequestURI()
	)

	app.Logger.Error(err.Error(), "method", method, "uri", uri)
}

func (app *Application) writeError(w http.ResponseWriter, r *http.Request, status int, message any) {
	env := envelope{"error": message}

	err := app.writeJSON(w, status, env, nil)
	if err != nil {
		app.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (app *Application) serverErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	app.logError(r, err)

	message := "the server encountered a problem and could not process your request"
	app.writeError(w, r, http.StatusInternalServerError, message)
}

func (app *Application) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	message := "the requested resource could not be found"
	app.writeError(w, r, http.StatusNotFound, message)
}

func (app *Application) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	message := fmt.Sprintf("the %s method is not supported for this resource", r.Method)
	app.writeError(w, r, http.StatusMethodNotAllowed, message)
}

func (app *Application) badRequestResponse(w http.ResponseWriter, r *http.Request, err error) {
	app.writeError(w, r, http.StatusBadRequest, err.Error())
}

func (app *Application) failedValidationResponse(w http.ResponseWriter, r *http.Request, errors map[string]string) {
	app.writeError(w, r, http.StatusUnprocessableEntity, errors)
}

func (app *Application) editConflictResponse(w http.ResponseWriter, r *http.Request) {
	message := "unable to update the record due to an edit conflict, please try again"
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) recordInUseResponse(w http.ResponseWriter, r *http.Request) {
	message := "the record is referenced by other records and cannot be deleted"
	app.writeError(w, r, http.StatusConflict, message)
}

// recordFrozenResponse answers the ADR-008 freeze gates: the write tried to
// move something back to an earlier stage while a match has already started.
// A separate message from recordInUseResponse — the conflict is temporal,
// not referential, and "try again later" would be wrong advice.
func (app *Application) recordFrozenResponse(w http.ResponseWriter, r *http.Request) {
	message := "a match has already started, so the hierarchy cannot move to an earlier stage"
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) duplicateRecordResponse(w http.ResponseWriter, r *http.Request) {
	message := "a record with these unique values already exists"
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) invalidCredentialsResponse(w http.ResponseWriter, r *http.Request) {
	message := "invalid authentication credentials"
	app.writeError(w, r, http.StatusUnauthorized, message)
}

func (app *Application) invalidAuthenticationTokenResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")

	message := "invalid or missing authentication"
	app.writeError(w, r, http.StatusUnauthorized, message)
}

func (app *Application) authenticationRequiredResponse(w http.ResponseWriter, r *http.Request) {
	message := "you must be authenticated to acces this resource"
	app.writeError(w, r, http.StatusUnauthorized, message)
}

func (app *Application) inactiveAcountResponse(w http.ResponseWriter, r *http.Request) {
	message := "your user account must be activated to access this resource"
	app.writeError(w, r, http.StatusForbidden, message)
}

func (app *Application) missingPermissionResponse(w http.ResponseWriter, r *http.Request) {
	message := "your user account doesn't have the necessary permissions to access this resource"
	app.writeError(w, r, http.StatusForbidden, message)
}

func (app *Application) forbiddenResponse(w http.ResponseWriter, r *http.Request) {
	message := "you do not have permission to modify this resource"
	app.writeError(w, r, http.StatusForbidden, message)
}

func (app *Application) rateLimitExceededResponse(w http.ResponseWriter, r *http.Request) {
	message := "rate limit exceeded"
	app.writeError(w, r, http.StatusTooManyRequests, message)
}

// roundNotOpenResponse refuses a ride command issued on a round that is not
// open. Distinct from recordFrozenResponse (temporal hierarchy freeze): the
// round exists and is valid, it just isn't accepting commands. Refusal
// happens here, before phase derivation — a closed round has no phase.
func (app *Application) roundNotOpenResponse(w http.ResponseWriter, r *http.Request, status game.RoundStatus) {
	message := fmt.Sprintf("ride commands require an open round, but this round is %s", status)
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

func (app *Application) invalidPhaseResponse(w http.ResponseWriter, r *http.Request, phase game.RoundPhase) {
	message := fmt.Sprintf("this ride command is not allowed while the round is in the %s phase", phase)
	app.writeError(w, r, http.StatusConflict, message)
}

func (app *Application) invalidStateTransitionResponse(w http.ResponseWriter, r *http.Request, state game.RideState) {
	message := fmt.Sprintf("this action is not allowed for a ride in the %s state", state)
	app.writeError(w, r, http.StatusConflict, message)
}
