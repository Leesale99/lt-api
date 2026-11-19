package httpx

import (
	"context"
	"net/http"
	"slices"
)

// The request context carries what the middleware chain resolved: the
// authenticated principal and, once a permission check has run, that role's
// permission set. Both are transport-level facts — the domain never learns
// how they were obtained.

type contextKey string

const authenticatedUserContextKey = contextKey("authenticatedUser")
const permissionsContextKey = contextKey("permissions")

// AuthUser is the authenticated principal as the HTTP layer needs it: the id
// handlers attribute writes to, the activation flag the permission gate
// enforces, and the role the permission lookup keys on. Deliberately not
// identity.User — httpx must not import a domain context; the adapter on the
// api side maps the real user onto this shape.
type AuthUser struct {
	ID        int
	Activated bool
	RoleID    int
}

// Permissions is a role's resolved permission codes. Include answers "does
// this set carry code".
type Permissions []string

// Include reports whether the set contains code.
// rule:R-IDENTITY-017
func (p Permissions) Include(code string) bool {
	return slices.Contains(p, code)
}

// SetPermissions returns r with the resolved permission set attached.
func SetPermissions(r *http.Request, permissions Permissions) *http.Request {
	ctx := context.WithValue(r.Context(), permissionsContextKey, permissions)
	return r.WithContext(ctx)
}

// GetPermissions returns the permission set resolved by the permission
// middleware. An empty set (not false) when absent: a handler that asks
// beyond what the middleware checked simply finds no permission, instead of
// having to treat "absent" as a third state.
func GetPermissions(r *http.Request) Permissions {
	permissions, ok := r.Context().Value(permissionsContextKey).(Permissions)
	if !ok {
		return Permissions{}
	}
	return permissions
}

// SetAuthenticatedUser returns r with the authenticated principal attached.
// rule:R-KERNEL-008
func SetAuthenticatedUser(r *http.Request, user AuthUser) *http.Request {
	ctx := context.WithValue(r.Context(), authenticatedUserContextKey, user)
	return r.WithContext(ctx)
}

// GetAuthenticatedUser returns the authenticated principal, if the request
// carried a valid token.
func GetAuthenticatedUser(r *http.Request) (AuthUser, bool) {
	user, ok := r.Context().Value(authenticatedUserContextKey).(AuthUser)
	return user, ok
}
