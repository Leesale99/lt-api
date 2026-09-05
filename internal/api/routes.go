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
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", app.showSeasonHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons", app.createSeasonHandler)

	// Rounds
	router.HandlerFunc(http.MethodGet, "/v1/rounds/:id", app.showRoundHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/rounds", app.createRoundHandler)

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", app.showMatchHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", app.createMatchHandler)

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", app.showTeamHandler)
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.createTeamHandler)

	return app.recoverPanic(router)
}
