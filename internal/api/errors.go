package api

// This file holds what is left of the response writers in api: the two the
// router registers by name. The module-flavored writers moved with their
// handlers — the schedule module's record-in-use/lifecycle-freeze pair and the
// game module's ride/idempotency/player pair — so the gateway keeps no rule of
// its own. The generic status writers live in httpx (httpx/errors.go); the
// envelope and its logging path are shared through httpx.WriteError, so
// nothing is duplicated.

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
