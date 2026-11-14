// Package identity is the authentication and authorization bounded context:
// users, their tokens, roles and permissions. It is the module's public
// facade (ADR-028, layered shape): this file is the only thing outside the
// module may import, and it exposes the composition-root constructor, the
// route table, the user-existence read the gateway's player handler needs,
// and — natively, no adapter — the two consumer-side interfaces the shared
// HTTP middleware declares (httpx.UserLookup, httpx.PermissionLookup).
//
// Everything else lives under internal/ — model, repository, service and
// handler — so the layers cannot leak across the boundary. The model is
// transport-free and storage-free; the repository translates Postgres facts
// into the model's sentinels; the service owns the activation transaction
// and the mail deliveries; the handler owns the frozen HTTP wire format.
//
// The module owns its authentication path: resolving a bearer token into the
// principal the request context carries (the AuthUser mapping), including the
// token-shape validation and the unknown/expired → 401 verdict, lives here
// rather than in a gateway adapter.
package identity

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/handler"
	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/identity/internal/repository"
	"lt-api.aleksrdvn.com/internal/identity/internal/service"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/validator"
)

// Mailer is the module's mail dependency: the interface declared at the
// service layer, re-exported so the composition root can name it. The
// concrete internal/shared/mailer satisfies it structurally.
type Mailer = service.Mailer

// Module is the identity bounded context as the rest of the process sees it.
// The composition root builds exactly one per process and hands the same
// value to the api gateway (which drives the routes and the two lookups).
type Module struct {
	svc *service.Service
}

// The middleware's consumer-side seams, satisfied natively by the facade.
var (
	_ httpx.UserLookup       = (*Module)(nil)
	_ httpx.PermissionLookup = (*Module)(nil)
)

// NewModule wires the identity module: the repositories over the identity
// pool, the use cases over them, and the mailer the registration and
// activation flows deliver through.
func NewModule(pool *pgxpool.Pool, mailer Mailer) *Module {
	repos := service.Repositories{
		Users:       repository.NewUserRepository(pool),
		UserTokens:  repository.NewUserTokenRepository(pool),
		Permissions: repository.NewPermissionRepository(pool),
	}

	return &Module{svc: service.New(pool, repos, mailer)}
}

// UseBackground installs the process background-task runner the mail
// deliveries are booked on (the shared server's tracker) — the composition
// root installs it after the server exists, because the tracker lives there.
func (m *Module) UseBackground(fn func(fn func(ctx context.Context))) {
	m.svc.UseBackground(fn)
}

// RegisterRoutes installs the module's routes on the gateway's router; paths
// and methods are the pre-refactor api/routes.go's, unchanged. These are the
// pre-authentication flows, so none sits behind a permission gate. The
// logger is the gateway's: the router and the log are the two things route
// registration needs from the owning process.
func (m *Module) RegisterRoutes(router handler.RouteRegistrar, log *slog.Logger) {
	m.svc.UseLogger(log)
	handler.New(m.svc, log).RegisterRoutes(router)
}

// UserForToken resolves an authentication-scoped bearer token to the
// principal the request context carries. Token shape is identity's rule, so
// it is checked here — before the query, as the middleware always did — and
// every refusal (malformed, unknown, expired) is httpx.ErrInvalidToken, the
// middleware's 401 verdict, rather than a concrete cause leaking to the
// transport layer.
func (m *Module) UserForToken(ctx context.Context, token string) (httpx.AuthUser, error) {
	v := validator.New()

	model.ValidateUserTokenPlaintext(v, token)
	if !v.Valid() {
		return httpx.AuthUser{}, httpx.ErrInvalidToken
	}

	user, err := m.svc.UserForToken(ctx, model.ScopeAuthentication, token)
	if err != nil {
		// A token that resolves to no row is the same verdict as a malformed
		// one: 401, never 500.
		if errors.Is(err, model.ErrNotFound) {
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

// PermissionsForRole resolves a role's permission codes — the permission
// gate's lookup.
func (m *Module) PermissionsForRole(ctx context.Context, roleID int) (httpx.Permissions, error) {
	permissions, err := m.svc.PermissionsForRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	return httpx.Permissions(permissions), nil
}

// User reports one user's existence — the read the gateway's player-create
// handler needs before writing its user reference (the cross-context FK is
// gone). ErrNotFound when the user is unknown.
func (m *Module) User(ctx context.Context, id int) (User, error) {
	return m.svc.User(ctx, id)
}

// Type aliases: the module's public vocabulary is the model's, re-exported so
// callers keep naming the identity types they always named. Aliases, not
// copies: there is one definition of every fact.
type (
	User        = model.User
	UserToken   = model.UserToken
	Permissions = model.Permissions
	Role        = model.Role
)

// The model's sentinels, re-exported: callers map them to their own
// responses (the gateway's player handler turns ErrNotFound into its
// favourite-team validation error).
var (
	ErrNotFound           = model.ErrNotFound
	ErrEditConflict       = model.ErrEditConflict
	ErrDuplicateEmail     = model.ErrDuplicateEmail
	ErrAlreadyActivated   = model.ErrAlreadyActivated
	ErrInvalidCredentials = model.ErrInvalidCredentials
)
