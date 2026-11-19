// Package httpchain owns the ONE place the served HTTP middleware chain is
// assembled. The nesting is the rule (R-PLATFORM-014): panic recovery
// outermost, then cross-origin handling, then throttling, then credential
// resolution, then the route. A caller that wants the production chain asks
// for it here instead of rebuilding it — the composition root (cmd/api) and
// the end-to-end suite both do, so a reordering breaks a test instead of
// leaving the suite green on a chain production does not build.
//
// The middleware itself lives in internal/kernel/httpx; this package owns only
// the order, and it takes the pieces as parameters so a caller may substitute
// what it legitimately decides (the origin allowlist, the limiter, the
// authentication seams) without touching the order.
package httpchain

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// Build wraps router in the production chain, outermost first:
//
//	RecoverPanic(EnableCORS(RateLimit(Authenticate(router))))
//
// chain carries the middleware's collaborators and router carries the route
// table plus the router's own 404/405 writers. A request therefore always has
// a panic answered, a preflight never spends a throttle token and never needs
// a credential, and a caller is throttled before any work is done on its
// behalf (R-PLATFORM-014).
func Build(chain *httpx.Middleware, router http.Handler) http.Handler {
	return chain.RecoverPanic(chain.EnableCORS(chain.RateLimit(chain.Authenticate(router))))
}
