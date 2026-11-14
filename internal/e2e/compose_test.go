package e2e

// compose_test.go is the ONE end-to-end composition helper (phase 6): it wires
// the real modules over the real test-database pools exactly like
// cmd/api/main.go does — configless, with the logger and the no-op mailer the
// only substitutions — and builds the same handler chain through the same
// route table. The old gateway package's per-test composition copy is gone;
// every E2E test calls this helper and then app.routes().

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/julienschmidt/httprouter"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/kernel/eventbus"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/idempotency"
	"lt-api.aleksrdvn.com/internal/ledger"
	"lt-api.aleksrdvn.com/internal/platform/server"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// testApplication is the composition root's state, one field per
// cmd/api/main.go local. Tests may tweak TrustedOrigins/RateLimiter before
// calling routes() — exactly the options main decides from config.
type testApplication struct {
	Server         *server.Server
	TrustedOrigins []string
	RateLimiter    *httpx.RateLimiter
	Game           *game.Module
	Schedule       *schedule.Module
	Identity       *identity.Module
}

// newTestApplication mirrors cmd/api/main.go: bus → schedule module → ledger →
// identity module → game module (with its identity directory) → the calendar's
// reference-port adapter. The pools are the per-context role-restricted pools
// TestMain opened; testPool (owner) exists for fixture planting only.
func newTestApplication() *testApplication {
	bus := eventbus.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	scheduleModule := schedule.NewModule(schedulePool, bus, log)
	ledgerRepo := ledger.New()
	identityModule := identity.NewModule(identityPool, nopMailer{})

	gameModule := game.NewModule(gamePool, scheduleModule, ledgerRepo, idempotency.New(), bus, log)
	gameModule.UseIdentity(identityModule)

	// The calendar's delete policies consult game through the same boundary
	// adapter main installs (game sentinels → schedule sentinels).
	scheduleModule.UseGameRefs(testGameRefs{game: gameModule})

	return &testApplication{
		Server:   &server.Server{Env: "test", Version: "test", Logger: log},
		Game:     gameModule,
		Schedule: scheduleModule,
		Identity: identityModule,
	}
}

// routes mirrors cmd/api/routes.go: the same router, the same registration
// order, the same router writers and the same middleware nesting. The
// healthcheck route is registered too, so the E2E suite sees the production
// route set — no test may pass on a chain main does not build.
func (app *testApplication) routes() http.Handler {
	chain := &httpx.Middleware{
		Logger:         app.Server.Logger,
		TrustedOrigins: app.TrustedOrigins,
		RateLimiter:    app.RateLimiter,
		Users:          app.Identity,
		Permissions:    app.Identity,
	}

	router := httprouter.New()

	router.NotFound = http.HandlerFunc(httpx.NotFound)
	router.MethodNotAllowed = http.HandlerFunc(httpx.MethodNotAllowed)

	// Health check
	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		data := httpx.Envelope{
			"status":      "available",
			"environment": app.Server.Env,
			"version":     app.Server.Version,
		}

		if err := httpx.WriteJSON(w, http.StatusOK, data, nil); err != nil {
			httpx.ServerError(w, r, app.Server.Logger, err)
		}
	})

	// Schedule module (seasons, rounds, matches, teams).
	app.Schedule.RegisterRoutes(router, chain.RequirePermission)

	// Game module (players, rides).
	app.Game.RegisterRoutes(router, chain.RequirePermission)

	// Users and User Tokens: pre-authentication flows, no permission gate.
	app.Identity.RegisterRoutes(router, app.Server.Logger)

	return chain.RecoverPanic(chain.EnableCORS(chain.RateLimit(chain.Authenticate(router))))
}

// testGameRefs adapts the game facade to the calendar's consumer-side port
// (schedule's service.GameUsage) — the same adapter cmd/api/gamerefs.go
// installs: game.ErrRecordInUse becomes schedule.ErrRecordInUse, and nothing
// else is translated.
type testGameRefs struct{ game *game.Module }

func (g testGameRefs) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.PlayersExistForTeam(ctx, teamID)
}

func (g testGameRefs) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.RidesExistForTeam(ctx, teamID)
}

func (g testGameRefs) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return g.game.RidesExistForMatch(ctx, matchID)
}

func (g testGameRefs) DeletePlayersForSeason(ctx context.Context, seasonID int) error {
	err := g.game.DeletePlayersForSeason(ctx, seasonID)
	if errors.Is(err, game.ErrRecordInUse) {
		return schedule.ErrRecordInUse
	}
	return err
}

// nopMailer stands in for the real SMTP client: handlers call Send, nothing
// leaves the process, and the background goroutine has nothing to panic on.
type nopMailer struct{}

func (nopMailer) Send(_ context.Context, recipient string, templateFile string, data any) error {
	return nil
}
