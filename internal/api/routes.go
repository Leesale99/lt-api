package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func (app *Application) routes() http.Handler {
	router := httprouter.New()

	router.NotFound = http.HandlerFunc(app.notFoundResponse)
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	// Health check
	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", app.healthcheckHandler)

	// Seasons
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", app.showSeasonsHandler)

	// Teams
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.createTeamHandler)

	return app.recoverPanic(router)
}
