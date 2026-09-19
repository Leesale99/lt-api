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
	router.HandlerFunc(http.MethodGet, "/v1/seasons", app.requirePermission("seasons:read", app.listSeasonsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", app.requirePermission("seasons:read", app.showSeasonHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons", app.requirePermission("seasons:write", app.createSeasonHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id", app.requirePermission("seasons:write", app.updateSeasonHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id", app.requirePermission("seasons:write", app.deleteSeasonHandler))

	// Rounds
	router.HandlerFunc(http.MethodGet, "/v1/rounds", app.requirePermission("rounds:read", app.listRoundsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/rounds/:id", app.requirePermission("rounds:read", app.showRoundHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/rounds", app.requirePermission("rounds:write", app.createRoundHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/rounds/:round_id", app.requirePermission("rounds:write", app.updateRoundHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/rounds/:round_id", app.requirePermission("rounds:write", app.deleteRoundHandler))

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches", app.requirePermission("matches:read", app.listMatchHandler))
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", app.requirePermission("matches:read", app.showMatchHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", app.requirePermission("matches:write", app.createMatchHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/matches/:match_id", app.requirePermission("matches:write", app.updateMatchHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/matches/:match_id", app.requirePermission("matches:write", app.deleteMatchHandler))

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams", app.requirePermission("teams:read", app.listTeamsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", app.requirePermission("teams:read", app.showTeamHandler))
	router.HandlerFunc(http.MethodPost, "/v1/teams", app.requirePermission("teams:write", app.createTeamHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/teams/:id", app.requirePermission("teams:write", app.updateTeamHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/teams/:id", app.requirePermission("teams:write", app.deleteTeamHandler))

	// Players
	router.HandlerFunc(http.MethodGet, "/v1/players", app.requirePermission("players:read", app.listPlayersHandler))
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", app.requirePermission("players:read", app.showPlayerHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", app.requirePermission("players:write", app.createPlayerHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:player_id", app.requirePermission("players:write", app.updatePlayerHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:player_id", app.requirePermission("players:write", app.deletePlayerHandler))

	// Users
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// User Tokens
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/activation", app.createActivationTokenHandler)
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/authentication", app.createAuthenticationTokenHandler)

	return app.recoverPanic(app.authenticate(router))
}
