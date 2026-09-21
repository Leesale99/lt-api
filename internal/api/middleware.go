package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			pv := recover()
			if pv != nil {
				w.Header().Set("Connection", "close")
				app.serverErrorResponse(w, r, fmt.Errorf("%v", pv))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func (app *Application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Authorization")

		authorizationHeader := r.Header.Get("Authorization")
		if authorizationHeader == "" {
			next.ServeHTTP(w, r)
			return
		}

		headerParts := strings.Split(authorizationHeader, " ")
		if len(headerParts) != 2 || headerParts[0] != "Bearer" {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		userToken := headerParts[1]

		v := validator.New()

		if identity.ValidateUserTokenPlaintext(v, userToken); !v.Valid() {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
		defer cancel()

		user, err := app.Identity.Users.GetForToken(ctx, identity.ScopeAuthentication, userToken)
		if err != nil {
			switch {
			case errors.Is(err, context.Canceled):
				return
			case errors.Is(err, store.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		r = app.contextSetAuthenticatedUser(r, user)

		next.ServeHTTP(w, r)
	})
}

func (app *Application) requirePermission(code string, next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticatedUser, found := app.contextGetAuthenticatedUser(r)
		if !found {
			app.authenticationRequiredResponse(w, r)
			return
		}

		if !authenticatedUser.Activated {
			app.inactiveAcountResponse(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
		defer cancel()

		permissions, err := app.Identity.Permissions.GetAllForRole(ctx, authenticatedUser.RoleID)
		if err != nil {
			app.serverErrorResponse(w, r, err)
			return
		}

		if !permissions.Include(code) {
			app.missingPermissionResponse(w, r)
			return
		}

		// Resolved set travels with the request so handlers can ask about
		// other codes (e.g. players:write:any) without re-querying the DB.
		next.ServeHTTP(w, app.contextSetPermissions(r, permissions))
	})
}

// rateLimit is the HTTP shell around RateLimiter: it keys on the
// kernel-controlled RemoteAddr (never client-supplied headers —
// X-Forwarded-For is attacker-controlled unless a trusted proxy guarantees
// it), and turns a denial into 429 + Retry-After. All state and decisions
// live in RateLimiter (ratelimiter.go); this function only translates
// between HTTP and Allow's verdict.
func (app *Application) rateLimit(next http.Handler) http.Handler {
	l := app.RateLimiter
	if l == nil || !l.Enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			app.serverErrorResponse(w, r, err)
			return
		}

		ok, retryAfter := l.Allow(ip)
		if !ok {
			// RFC 7231 delay-seconds: an integer count of seconds, ceil'd —
			// rounding down would promise a retry before the token exists.
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			app.rateLimitExceededResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// enableCORS implements the origin policy: exact-match allowlist from
// config (TrustedOrigins), never reflection and never "*". CORS is
// browser-enforced, not server security — curl and server-to-server calls
// send no Origin and are unaffected; a disallowed origin simply gets no
// CORS headers, so the browser blocks the response while the request
// itself completes (silent rejection, chosen over an explicit 403).
//
// Chain position — recoverPanic(enableCORS(rateLimit(authenticate(...)))):
// an allowed preflight short-circuits here, before rateLimit (a preflight
// must not burn a token) and before authenticate (it carries no
// Authorization header); recoverPanic stays outermost.
//
// Allowed methods/headers: GET/HEAD/POST and the safelisted request
// headers are always permitted by the CORS-safelist and need no
// declaration; the lists below add everything else this API serves
// cross-origin (PUT/PATCH/DELETE) and the non-safelisted headers its
// clients send (Authorization; Content-Type beyond the safelisted form
// values, e.g. application/json).
func (app *Application) enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")

		if origin != "" {
			for i := range app.TrustedOrigins {
				if origin == app.TrustedOrigins[i] {
					w.Header().Set("Access-Control-Allow-Origin", origin)

					if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
						w.Header().Set("Access-Control-Allow-Methods", "PUT, PATCH, DELETE")
						w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
						// Preflight caching: one day without re-preflighting. The
						// staleness risk is a redeployed allowlist lagging behind on
						// clients — acceptable at this deploy cadence.
						w.Header().Set("Access-Control-Max-Age", "86400")

						w.WriteHeader(http.StatusOK)
						return
					}

					break
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}
