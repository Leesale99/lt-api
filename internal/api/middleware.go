package api

// The three middleware methods routes.go registers are one-line delegations:
// the chain itself lives in httpx (httpx.Middleware), the wiring — logger,
// CORS allowlist, limiter, identity module — is Application's.

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/shared/httpx"
)

// middleware is the httpx chain state for this application. It carries no
// per-request state; the identity seam is the module's facade, which
// satisfies both consumer-side lookups natively.
func (app *Application) middleware() *httpx.Middleware {
	return &httpx.Middleware{
		Logger:         app.Logger,
		TrustedOrigins: app.TrustedOrigins,
		RateLimiter:    app.RateLimiter,
		Users:          app.Identity,
		Permissions:    app.Identity,
	}
}

func (app *Application) recoverPanic(next http.Handler) http.Handler {
	return app.middleware().RecoverPanic(next)
}

func (app *Application) authenticate(next http.Handler) http.Handler {
	return app.middleware().Authenticate(next)
}

func (app *Application) requirePermission(code string, next http.HandlerFunc) http.HandlerFunc {
	return app.middleware().RequirePermission(code, next)
}

func (app *Application) rateLimit(next http.Handler) http.Handler {
	return app.middleware().RateLimit(next)
}

func (app *Application) enableCORS(next http.Handler) http.Handler {
	return app.middleware().EnableCORS(next)
}
