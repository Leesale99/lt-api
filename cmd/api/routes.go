package main

// routes.go is the HTTP composition: the router, the module route tables, the
// router-level writers and the middleware chain — in that order. It lives at
// the composition root because it needs the modules, the shared http chain and
// the server's logger at once; the api gateway package that used to own it is
// gone (phase 6), and nothing else builds a router.

import (
	"net/http"

	"github.com/julienschmidt/httprouter"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/platform/httpchain"
	"lt-api.aleksrdvn.com/internal/platform/server"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// routes builds the fully-served handler chain. The registration order and
// the chain nesting are the pre-refactor api/routes.go's, unchanged — the wire
// is frozen:
//
//	RecoverPanic(EnableCORS(RateLimit(Authenticate(router))))
//
// with the router's own writers for 404/405 and one GET route per the
// healthcheck plus the three module route tables. The nesting itself is
// httpchain.Build's (one builder, shared with the end-to-end suite, so the two
// chains cannot drift — R-PLATFORM-014).
func routes(srv *server.Server, chain *httpx.Middleware, calendar *schedule.Module, gameplay *game.Module, users *identity.Module) http.Handler {
	router := httprouter.New()

	// The router's own writers: unknown path → 404, known path + wrong method
	// → 405, both in the shared envelope.
	router.NotFound = http.HandlerFunc(httpx.NotFound)
	router.MethodNotAllowed = http.HandlerFunc(httpx.MethodNotAllowed)

	// Health check
	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", healthcheckHandler(srv))

	// Schedule module (seasons, rounds, matches, teams): the calendar context
	// owns its own routes — paths, methods and permission gate unchanged.
	calendar.RegisterRoutes(router, chain.RequirePermission)

	// Game module (players, rides): likewise, behind the same gate.
	gameplay.RegisterRoutes(router, chain.RequirePermission)

	// Users and User Tokens: the identity context owns its pre-authentication
	// flows (register, activate, token issuance) — none sits behind a
	// permission gate. The logger is the process logger, as before.
	users.RegisterRoutes(router, srv.Logger)

	return httpchain.Build(chain, router)
}
