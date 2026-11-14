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

	// Game module (players, rides): the gameplay context owns its own routes —
	// paths, methods and permission gate are unchanged, the module registers
	// them itself.
	app.Game.RegisterRoutes(router, app.requirePermission)

	// Users and User Tokens: the identity context owns its own routes
	// (register, activate, token issuance) — these are the pre-authentication
	// flows, so none sits behind a permission gate; paths and methods are the
	// pre-refactor routes.go's, unchanged.
	app.Identity.RegisterRoutes(router, app.Logger)

	return app.recoverPanic(app.enableCORS(app.rateLimit(app.authenticate(router))))
}
