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
	router.HandlerFunc(http.MethodGet, "/v1/seasons", app.requireActivatedUser(app.listSeasonsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", app.requireActivatedUser(app.showSeasonHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons", app.requireActivatedUser(app.createSeasonHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id", app.requireActivatedUser(app.updateSeasonHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id", app.requireActivatedUser(app.deleteSeasonHandler))

	// Rounds
	router.HandlerFunc(http.MethodGet, "/v1/rounds", app.requireActivatedUser(app.listRoundsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/rounds/:id", app.requireActivatedUser(app.showRoundHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/rounds", app.requireActivatedUser(app.createRoundHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/rounds/:round_id", app.requireActivatedUser(app.updateRoundHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/rounds/:round_id", app.requireActivatedUser(app.deleteRoundHandler))

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches", app.requireActivatedUser(app.listMatchHandler))
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", app.requireActivatedUser(app.showMatchHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", app.requireActivatedUser(app.createMatchHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/matches/:match_id", app.requireActivatedUser(app.updateMatchHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/matches/:match_id", app.requireActivatedUser(app.deleteMatchHandler))

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams", app.requireActivatedUser(app.listTeamsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", app.requireActivatedUser(app.showTeamHandler))
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.requireActivatedUser(app.createTeamHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/teams/:id", app.requireActivatedUser(app.updateTeamHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/teams/:id", app.requireActivatedUser(app.deleteTeamHandler))

	// Players
	router.HandlerFunc(http.MethodGet, "/v1/players", app.requireActivatedUser(app.listPlayersHandler))
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", app.requireActivatedUser(app.showPlayerHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", app.requireActivatedUser(app.createPlayerHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:player_id", app.requireActivatedUser(app.updatePlayerHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:player_id", app.requireActivatedUser(app.deletePlayerHandler))

	// Users
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// User Tokens
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/activation", app.createActivationTokenHandler)
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/authentication", app.createAuthenticationTokenHandler)

	return app.recoverPanic(app.authenticate(router))
}
