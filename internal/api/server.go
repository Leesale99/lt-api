package api

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/server"
)

// Application is the HTTP gateway: the module dependencies the handlers
// drive and the inputs the handler chain is built from. Process lifecycle
// (http.Server, signals, drain, background-task tracking) is the embedded
// shared/server.Server's — api composes the chain and hands it over, nothing
// here knows how the process stops.
type Application struct {
	*server.Server

	// TrustedOrigins is the CORS origin allowlist (from cfg.CORS.Origins).
	// Nil means CORS is disabled: the middleware matches nothing and no
	// CORS headers are ever emitted — "not allowed", never "allow all".
	TrustedOrigins []string
	// RateLimiter is nil when rate limiting is not wired (handler tests);
	// constructed with Enabled=false it passes everything through.
	RateLimiter *httpx.RateLimiter

	// Game is the ride domain's service: the entry point to ride commands.
	// Handlers call domain operations on it; ride/player CRUD that carries
	// no rules goes through Game.Store.
	//
	// Schedule is the calendar context's module (its own context since the
	// Game/Schedule split): it registers its own routes (seasons, rounds,
	// matches, teams), and the player handlers read its season/team
	// existence surface. The module also satisfies game's SchedulePort and
	// publishes the event seam's payloads.
	//
	// Identity is the authentication context's module: it registers its own
	// routes (users, user-tokens), owns its mailer, and satisfies httpx's
	// UserLookup/PermissionLookup natively (no gateway adapter), so the
	// middleware chain is built from the module itself.
	Game     *game.Service
	Schedule *schedule.Module
	Identity *identity.Module
}

// HandlerChain builds the full handler chain (router + middleware) the
// shared server serves. It is a pure builder: never start goroutines here.
func (app *Application) HandlerChain() http.Handler { return app.routes() }

// Serve starts the api-owned background work that lives exactly as long as
// serving (the rate-limiter sweep, booked on the server's tracker so the
// shutdown drain waits it out) and then runs the shared server lifecycle.
func (app *Application) Serve() error {
	if app.RateLimiter != nil && app.RateLimiter.Enabled {
		app.background(app.RateLimiter.Sweep)
	}

	return app.Server.Serve()
}
