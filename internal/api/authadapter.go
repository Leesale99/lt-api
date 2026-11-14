package api

// The identity adapter: the temporary seam between the HTTP layer and the
// identity context. httpx must not import identity, so it declares
// consumer-side interfaces (httpx.UserLookup, httpx.PermissionLookup) and
// this file maps the identity store onto them. It is replaced by the identity
// facade in a later phase.

import (
	"context"
	"errors"

	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

// identityAuth adapts identity.Store to httpx's two lookups.
type identityAuth struct {
	store *identity.Store
}

var (
	_ httpx.UserLookup       = identityAuth{}
	_ httpx.PermissionLookup = identityAuth{}
)

// UserForToken resolves an authentication-scoped bearer token. Token shape is
// identity's rule, so it is checked here — before the query, as the middleware
// always did — and reported as httpx.ErrInvalidToken (the 401 verdict) rather
// than leaking the concrete cause to the transport layer.
func (a identityAuth) UserForToken(ctx context.Context, token string) (httpx.AuthUser, error) {
	v := validator.New()

	if identity.ValidateUserTokenPlaintext(v, token); !v.Valid() {
		return httpx.AuthUser{}, httpx.ErrInvalidToken
	}

	user, err := a.store.Users.GetForToken(ctx, identity.ScopeAuthentication, token)
	if err != nil {
		// A token that resolves to no row is the same verdict as a malformed
		// one: 401, never 500.
		if errors.Is(err, persistence.ErrRecordNotFound) {
			return httpx.AuthUser{}, httpx.ErrInvalidToken
		}
		return httpx.AuthUser{}, err
	}

	return httpx.AuthUser{
		ID:        user.ID,
		Activated: user.Activated,
		RoleID:    user.RoleID,
	}, nil
}

// PermissionsForRole resolves a role's permission codes.
func (a identityAuth) PermissionsForRole(ctx context.Context, roleID int) (httpx.Permissions, error) {
	permissions, err := a.store.Permissions.GetAllForRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	return httpx.Permissions(permissions), nil
}
