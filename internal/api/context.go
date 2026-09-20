package api

import (
	"context"
	"net/http"

	"lt-api.aleksrdvn.com/internal/identity"
)

type contextKey string

const authenticatedUserContextKey = contextKey("authenticatedUser")
const permissionsContextKey = contextKey("permissions")

func (app *Application) contextSetPermissions(r *http.Request, permissions identity.Permissions) *http.Request {
	ctx := context.WithValue(r.Context(), permissionsContextKey, permissions)
	return r.WithContext(ctx)
}

// contextGetPermissions returns the permission set resolved by the
// requirePermission middleware. An empty set (not false) when absent: a
// handler that asks beyond what the middleware checked simply finds no
// permission, instead of having to treat "absent" as a third state.
func (app *Application) contextGetPermissions(r *http.Request) identity.Permissions {
	permissions, ok := r.Context().Value(permissionsContextKey).(identity.Permissions)
	if !ok {
		return identity.Permissions{}
	}
	return permissions
}

func (app *Application) contextSetAuthenticatedUser(r *http.Request, user identity.User) *http.Request {
	ctx := context.WithValue(r.Context(), authenticatedUserContextKey, user)
	return r.WithContext(ctx)
}

func (app *Application) contextGetAuthenticatedUser(r *http.Request) (identity.User, bool) {
	user, ok := r.Context().Value(authenticatedUserContextKey).(identity.User)
	return user, ok
}
