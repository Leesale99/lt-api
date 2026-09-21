package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
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

func (app *Application) rateLimit(next http.Handler) http.Handler {
	if !app.Limiter.Enabled {
		return next
	}

	type client struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu      sync.Mutex
		clients = make(map[string]*client)
	)

	app.background(func(ctx context.Context) {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()

				for ip, client := range clients {
					if time.Since(client.lastSeen) > constants.RateLimitCleanupInterval {
						delete(clients, ip)
					}
				}

				mu.Unlock()
			}
		}
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := strings.Split(r.RemoteAddr, ":")[0]

		mu.Lock()

		if _, found := clients[ip]; !found {
			clients[ip] = &client{
				limiter: rate.NewLimiter(rate.Limit(app.Limiter.Rps), app.Limiter.Burst),
			}
		}

		if !clients[ip].limiter.Allow() {
			mu.Unlock()

			reserved := clients[ip].limiter.Reserve().Delay().Seconds()

			w.Header().Add("Retry-After", fmt.Sprintf("%v seconds", reserved))

			app.rateLimitExceededResponse(w, r)
			return
		}

		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}
