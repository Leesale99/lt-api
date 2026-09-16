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
	router.HandlerFunc(http.MethodGet, "/v1/seasons", app.listSeasonsHandler)
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", app.showSeasonHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons", app.createSeasonHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id", app.updateSeasonHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id", app.deleteSeasonHandler)

	// Rounds
	router.HandlerFunc(http.MethodGet, "/v1/rounds", app.listRoundsHandler)
	router.HandlerFunc(http.MethodGet, "/v1/rounds/:id", app.showRoundHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/rounds", app.createRoundHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/rounds/:roundId", app.updateRoundHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/rounds/:roundId", app.deleteRoundHandler)

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", app.showMatchHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", app.createMatchHandler)

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams", app.listTeamsHandler)
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", app.showTeamHandler)
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.createTeamHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/teams/:id", app.updateTeamHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/teams/:id", app.deleteTeamHandler)

	router.HandlerFunc(http.MethodGet, "/v1/players", app.listPlayersHandler)
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", app.showPlayerHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", app.createPlayerHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:playerId", app.updatePlayerHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:playerId", app.deletePlayerHandler)

	return app.recoverPanic(router)
}
