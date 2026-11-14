// Package api implements the HTTP surface of the service: route wiring,
// request parsing and validation helpers, authentication and permission
// middleware, and the shared JSON envelope writers every handler uses.
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

	// Schedule module (seasons, rounds, matches, teams): the calendar
	// context owns its own routes — paths, methods and permission gate are
	// unchanged, the module just registers them itself.
	app.Schedule.RegisterRoutes(router, app.requirePermission)

	// Players
	router.HandlerFunc(http.MethodGet, "/v1/players", app.requirePermission("players:read", app.listPlayersHandler))
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", app.requirePermission("players:read", app.showPlayerHandler))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", app.requirePermission("players:write", app.createPlayerHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:player_id", app.requirePermission("players:write", app.updatePlayerHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:player_id", app.requirePermission("players:write", app.deletePlayerHandler))

	// Rides
	router.HandlerFunc(http.MethodGet, "/v1/rides", app.listRidesHandler)
	router.HandlerFunc(http.MethodGet, "/v1/rides/:id", app.showRideHandler)
	router.HandlerFunc(http.MethodPost, "/v1/rides", app.createRideHandler)
	// Every ride command is idempotent (ADR-024): one shared handler takes
	// the endpoint tag for the dedup identity and the service command.
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/lock", app.rideCommandHandler("POST /v1/rides/:id/lock", app.Game.RideLock))
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/burn", app.rideCommandHandler("POST /v1/rides/:id/burn", app.Game.RideBurn))
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/unlock", app.rideCommandHandler("POST /v1/rides/:id/unlock", app.Game.RideUnlock))

	// Users and User Tokens: the identity context owns its own routes
	// (register, activate, token issuance) — these are the pre-authentication
	// flows, so none sits behind a permission gate; paths and methods are the
	// pre-refactor routes.go's, unchanged.
	app.Identity.RegisterRoutes(router, app.Logger)

	return app.recoverPanic(app.enableCORS(app.rateLimit(app.authenticate(router))))
}
