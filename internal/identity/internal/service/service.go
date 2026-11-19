// Package service holds the identity context's use cases — the layer between
// the HTTP handlers (and the module facade) and the repositories. Plain reads
// pass through; the use cases that carry a rule or a side effect live here:
// the activation transaction (activation.go), registration, and the two token
// issuance flows, which also deliver the transactional email.
package service

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/identity/internal/repository"
)

// Repositories is the storage set the service drives. The composition root
// builds them once and hands them over here.
type Repositories struct {
	Users       *repository.UserRepository
	UserTokens  *repository.UserTokenRepository
	Permissions *repository.PermissionRepository
}

// Service is identity's use-case layer. The pool exists for exactly one
// reason: the activation use case owns a transaction that spans the user
// row and its tokens. The mailer delivers the welcome/activation email;
// background is the process lifecycle's tracked-goroutine runner (installed
// by the composition root) and log is the process logger — both are
// nil-safe so a bare service in a unit test still works.
type Service struct {
	pool        *pgxpool.Pool
	users       *repository.UserRepository
	userTokens  *repository.UserTokenRepository
	permissions *repository.PermissionRepository

	mailer     Mailer
	background func(fn func(ctx context.Context))
	log        *slog.Logger
}

// New wires the use cases onto the composition root's repositories and
// mailer.
func New(pool *pgxpool.Pool, repos Repositories, mailer Mailer) *Service {
	return &Service{
		pool:        pool,
		users:       repos.Users,
		userTokens:  repos.UserTokens,
		permissions: repos.Permissions,
		mailer:      mailer,
	}
}

// UseBackground installs the process background-task runner the mail
// deliveries are booked on (the shared server's tracker, so shutdown drains
// them). Without one, deliveries run inline — correct, just not tracked.
func (s *Service) UseBackground(fn func(fn func(ctx context.Context))) {
	s.background = fn
}

// UseLogger installs the process logger for background delivery failures
// (the client is gone by the time the send runs, so there is no response to
// report through). Nil-safe: without one, failures are dropped.
func (s *Service) UseLogger(log *slog.Logger) {
	s.log = log
}

// User reports one user's existence — the read the gateway's player-create
// handler needs before writing its user reference. model.ErrNotFound when
// the user is unknown.
func (s *Service) User(ctx context.Context, id int) (model.User, error) {
	return s.users.Get(ctx, id)
}

// UserForToken resolves a scoped bearer token to its user.
// model.ErrNotFound when the token is unknown, expired or out of scope.
func (s *Service) UserForToken(ctx context.Context, scope, tokenPlaintext string) (model.User, error) {
	return s.users.GetForToken(ctx, scope, tokenPlaintext)
}

// PermissionsForRole resolves a role's permission codes — the permission
// gate's lookup.
func (s *Service) PermissionsForRole(ctx context.Context, roleID int) (model.Permissions, error) {
	return s.permissions.GetAllForRole(ctx, roleID)
}
