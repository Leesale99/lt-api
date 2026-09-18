package api

import (
	"context"
	"net/http"

	"lt-api.aleksrdvn.com/internal/identity"
)

type contextKey string

const authenticatedUserContextKey = contextKey("authenticatedUser")

func (app *Application) contextSetAuthenticatedUser(r *http.Request, user identity.User) *http.Request {
	ctx := context.WithValue(r.Context(), authenticatedUserContextKey, user)
	return r.WithContext(ctx)
}

func (app *Application) contextGetAuthenticatedUser(r *http.Request) (identity.User, bool) {
	user, ok := r.Context().Value(authenticatedUserContextKey).(identity.User)
	return user, ok
}
