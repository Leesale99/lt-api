package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"lt-api.aleksrdvn.com/internal/constants"
)

// ErrInvalidToken is the verdict a UserLookup returns for a bearer token it
// refuses (malformed, unknown, expired): the middleware answers 401 and never
// looks at the underlying cause.
var ErrInvalidToken = errors.New("invalid authentication token")

// UserLookup resolves a bearer token to the principal it authenticates. It is
// declared here, at the consumer, so the transport layer stays free of any
// identity import: the composition root adapts its user store to this shape.
type UserLookup interface {
	UserForToken(ctx context.Context, token string) (AuthUser, error)
}

// PermissionLookup resolves a role's permission codes — the second half of
// the same consumer-side seam.
type PermissionLookup interface {
	PermissionsForRole(ctx context.Context, roleID int) (Permissions, error)
}

// Middleware is the state the HTTP chain's middleware share. Application
// state stays with the caller: Middleware only knows what transport needs
// (a logger, the CORS allowlist, the limiter, the two lookups above).
type Middleware struct {
	Logger *slog.Logger
	// TrustedOrigins is the CORS origin allowlist. Nil means CORS is
	// disabled: the middleware matches nothing and no CORS headers are ever
	// emitted — "not allowed", never "allow all".
	TrustedOrigins []string
	// RateLimiter nil (or a disabled one) passes everything through.
	RateLimiter *RateLimiter
	// Users and Permissions are the authentication seams; nil is only
	// valid for chains that never reach Authenticate/RequirePermission.
	Users       UserLookup
	Permissions PermissionLookup
}

func (m *Middleware) RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			pv := recover()
			if pv != nil {
				w.Header().Set("Connection", "close")
				ServerError(w, r, m.Logger, fmt.Errorf("%v", pv))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// Authenticate resolves the bearer token (when present) into the request
// context; a request without one proceeds unauthenticated, and the
// permission gate is what refuses it later.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Authorization")

		authorizationHeader := r.Header.Get("Authorization")
		if authorizationHeader == "" {
			next.ServeHTTP(w, r)
			return
		}

		headerParts := strings.Split(authorizationHeader, " ")
		if len(headerParts) != 2 || headerParts[0] != "Bearer" {
			InvalidAuthenticationToken(w, r)
			return
		}

		userToken := headerParts[1]

		ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
		defer cancel()

		user, err := m.Users.UserForToken(ctx, userToken)
		if err != nil {
			switch {
			case errors.Is(err, context.Canceled):
				return
			case errors.Is(err, ErrInvalidToken):
				InvalidAuthenticationToken(w, r)
			default:
				ServerError(w, r, m.Logger, err)
			}
			return
		}

		r = SetAuthenticatedUser(r, user)

		next.ServeHTTP(w, r)
	})
}

// RequirePermission is the gate: an authenticated, activated principal whose
// role carries code may proceed; anything else is refused before the handler
// runs.
func (m *Middleware) RequirePermission(code string, next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticatedUser, found := GetAuthenticatedUser(r)
		if !found {
			AuthenticationRequired(w, r)
			return
		}

		if !authenticatedUser.Activated {
			InactiveAccount(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
		defer cancel()

		permissions, err := m.Permissions.PermissionsForRole(ctx, authenticatedUser.RoleID)
		if err != nil {
			ServerError(w, r, m.Logger, err)
			return
		}

		if !permissions.Include(code) {
			MissingPermission(w, r)
			return
		}

		// Resolved set travels with the request so handlers can ask about
		// other codes (e.g. players:write:any) without re-querying the DB.
		next.ServeHTTP(w, SetPermissions(r, permissions))
	})
}

// RateLimit is the HTTP shell around RateLimiter: it keys on the
// kernel-controlled RemoteAddr (never client-supplied headers —
// X-Forwarded-For is attacker-controlled unless a trusted proxy guarantees
// it), and turns a denial into 429 + Retry-After. All state and decisions
// live in RateLimiter (ratelimiter.go); this function only translates
// between HTTP and Allow's verdict.
func (m *Middleware) RateLimit(next http.Handler) http.Handler {
	l := m.RateLimiter
	if l == nil || !l.Enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ServerError(w, r, m.Logger, err)
			return
		}

		ok, retryAfter := l.Allow(ip)
		if !ok {
			// RFC 7231 delay-seconds: an integer count of seconds, ceil'd —
			// rounding down would promise a retry before the token exists.
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			TooManyRequests(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// EnableCORS implements the origin policy: exact-match allowlist from
// config (TrustedOrigins), never reflection and never "*". CORS is
// browser-enforced, not server security — curl and server-to-server calls
// send no Origin and are unaffected; a disallowed origin simply gets no
// CORS headers, so the browser blocks the response while the request
// itself completes (silent rejection, chosen over an explicit 403).
//
// Chain position — RecoverPanic(EnableCORS(RateLimit(Authenticate(...)))):
// an allowed preflight short-circuits here, before RateLimit (a preflight
// must not burn a token) and before Authenticate (it carries no
// Authorization header); RecoverPanic stays outermost.
//
// Allowed methods/headers: GET/HEAD/POST and the safelisted request
// headers are always permitted by the CORS-safelist and need no
// declaration; the lists below add everything else this API serves
// cross-origin (PUT/PATCH/DELETE) and the non-safelisted headers its
// clients send (Authorization; Content-Type beyond the safelisted form
// values, e.g. application/json).
func (m *Middleware) EnableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")

		if origin != "" {
			for i := range m.TrustedOrigins {
				if origin == m.TrustedOrigins[i] {
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
