package api

import (
	"fmt"
	"net/http"
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
