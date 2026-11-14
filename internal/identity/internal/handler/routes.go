package handler

import (
	"log/slog"
	"net/http"

	"lt-api.aleksrdvn.com/internal/identity/internal/service"
)

// Handler is identity's HTTP surface: the use cases and the logger the
// shared envelope writers log through. It holds no per-request state.
type Handler struct {
	svc *service.Service
	log *slog.Logger
}

func New(svc *service.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RouteRegistrar is the one capability the route table needs from the
// gateway's router (httprouter.Router satisfies it structurally, so the
// module does not depend on the router implementation).
type RouteRegistrar interface {
	HandlerFunc(method, path string, handler http.HandlerFunc)
}

// RegisterRoutes installs identity's routes — paths, methods and the
// absence of a permission gate are the pre-refactor api/routes.go's,
// unchanged. These are the pre-authentication flows (register, activate,
// obtain a token), so none of them sits behind a permission code; a
// [PermissionGate] parameter would be dead, so the module does not take one.
func (h *Handler) RegisterRoutes(router RouteRegistrar) {
	// Users
	router.HandlerFunc(http.MethodPost, "/v1/users", h.registerUser)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", h.activateUser)

	// User Tokens
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/activation", h.createActivationToken)
	router.HandlerFunc(http.MethodPost, "/v1/user-tokens/authentication", h.createAuthenticationToken)
}
