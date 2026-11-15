package handler

import (
	"log/slog"
	"net/http"

	"lt-api.aleksrdvn.com/internal/schedule/internal/service"
)

// Handler is schedule's HTTP surface: the use cases and the logger the
// shared envelope writers log through. It holds no per-request state.
type Handler struct {
	svc *service.Service
	log *slog.Logger
}

func New(svc *service.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// PermissionGate is the route middleware the module's route table composes
// each handler with: the api gateway's permission check, injected so this
// module never learns the identity seam. The shape is
// *httpx.Middleware.RequirePermission's.
type PermissionGate func(code string, next http.HandlerFunc) http.HandlerFunc

// RouteRegistrar is the one capability the route table needs from the
// gateway's router (httprouter.Router satisfies it structurally, so the
// module does not depend on the router implementation).
type RouteRegistrar interface {
	HandlerFunc(method, path string, handler http.HandlerFunc)
}

// RegisterRoutes installs schedule's routes — paths, methods and middleware
// order are the pre-refactor api/routes.go's, unchanged.
func (h *Handler) RegisterRoutes(router RouteRegistrar, gate PermissionGate) {
	// Seasons
	router.HandlerFunc(http.MethodGet, "/v1/seasons", gate("seasons:read", h.listSeasons))
	router.HandlerFunc(http.MethodGet, "/v1/seasons/:id", gate("seasons:read", h.showSeason))
	router.HandlerFunc(http.MethodPost, "/v1/seasons", gate("seasons:write", h.createSeason))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id", gate("seasons:write", h.updateSeason))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id", gate("seasons:write", h.deleteSeason))

	// Rounds
	router.HandlerFunc(http.MethodGet, "/v1/rounds", gate("rounds:read", h.listRounds))
	router.HandlerFunc(http.MethodGet, "/v1/rounds/:id", gate("rounds:read", h.showRound))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/rounds", gate("rounds:write", h.createRound))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/rounds/:round_id", gate("rounds:write", h.updateRound))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/rounds/:round_id", gate("rounds:write", h.deleteRound))

	// Matches
	router.HandlerFunc(http.MethodGet, "/v1/matches", gate("matches:read", h.listMatches))
	router.HandlerFunc(http.MethodGet, "/v1/matches/:id", gate("matches:read", h.showMatch))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/matches", gate("matches:write", h.createMatch))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/matches/:match_id", gate("matches:write", h.updateMatch))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/matches/:match_id", gate("matches:write", h.deleteMatch))
	// Resolve is schedule's script: it commits the result and
	// publishes MatchResultRecorded — the ride settlement is game's, behind
	// the event seam.
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/matches/:match_id/resolve", gate("matches:write", h.resolveMatch))

	// Teams
	router.HandlerFunc(http.MethodGet, "/v1/teams", gate("teams:read", h.listTeams))
	router.HandlerFunc(http.MethodGet, "/v1/teams/:id", gate("teams:read", h.showTeam))
	router.HandlerFunc(http.MethodPost, "/v1/teams", gate("teams:write", h.createTeam))
	router.HandlerFunc(http.MethodPatch, "/v1/teams/:id", gate("teams:write", h.updateTeam))
	router.HandlerFunc(http.MethodDelete, "/v1/teams/:id", gate("teams:write", h.deleteTeam))
}
