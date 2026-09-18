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
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/rounds/:round_id", app.updateRoundHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/rounds/:round_id", app.deleteRoundHandler)

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches", app.listMatchHandler)
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", app.showMatchHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", app.createMatchHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/matches/:match_id", app.updateMatchHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/matches/:match_id", app.deleteMatchHandler)

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams", app.listTeamsHandler)
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", app.showTeamHandler)
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.createTeamHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/teams/:id", app.updateTeamHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/teams/:id", app.deleteTeamHandler)

	// Players
	router.HandlerFunc(http.MethodGet, "/v1/players", app.listPlayersHandler)
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", app.showPlayerHandler)
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", app.createPlayerHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:player_id", app.updatePlayerHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:player_id", app.deletePlayerHandler)

	// Users
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// User Tokens
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/activation", app.createActivationTokenHandler)
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/authentication", app.createAuthenticationTokenHandler)

	return app.recoverPanic(app.authenticate(router))
}
