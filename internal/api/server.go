package api

import (
	"context"
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
	// Schedule is the calendar context's service (its own context since the
	// Game/Schedule split): the seasons/rounds/matches/teams handlers drive
	// its stores directly (ADR-028 — schedule CRUD carries no rules beyond
	// its validators), the player handlers read it for the season/team
	// existence checks, and the resolve PATCH runs its ResolveMatch script,
	// which commits the result and hands it to game as a
	// MatchResultRecorded event.
	Game     *game.Service
	Schedule *schedule.Service
	Identity *identity.Store
	Mailer   MailSender
}

// MailSender is everything the gateway needs from the mailer: one method.
// Declaring the interface here (at the consumer, not next to the concrete
// type) keeps api decoupled from SMTP entirely — tests inject a no-op
// instead of a live client.
type MailSender interface {
	Send(ctx context.Context, recipient string, templateFile string, data any) error
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
