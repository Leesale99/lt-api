package http

// The game module's HTTP surface: the ride and player endpoints, the wire
// DTOs and the game-flavored error mapping. It is the inbound adapter — it
// translates a request into a use-case call and the answer back into the
// frozen wire shape, and holds no rule of its own.
//
// Paths, methods and middleware are the pre-refactor api/routes.go's,
// unchanged: the module owns its wire surface, the gateway owns the router
// and the middleware chain around it.

import (
	"log/slog"
	"net/http"

	"lt-api.aleksrdvn.com/internal/game/internal/core/service"
)

// Handler is game's HTTP surface: the use cases and the logger the shared
// envelope writers log through. It holds no per-request state.
type Handler struct {
	svc   *service.Service
	log   *slog.Logger
	users UserDirectory
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

func New(svc *service.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// UseIdentity installs the identity read the player-registration use case's
// transport performs: the authenticated principal's user row must exist
// before game writes its reference (the cross-context FK is gone, migration
// 000016). It is a transport-level composition (identity is another context,
// and the check is read-only), so the module takes it after construction —
// the same shape the gateway passes for its own lookups.
func (h *Handler) UseIdentity(users UserDirectory) {
	h.users = users
}

// RegisterRoutes installs game's routes — paths and methods are the
// pre-refactor api/routes.go's, and every route carries its permission gate
// (the rides note below records why the ride surface takes the player codes).
// rule:R-IDENTITY-017
func (h *Handler) RegisterRoutes(router RouteRegistrar, gate PermissionGate) {
	// Players
	router.HandlerFunc(http.MethodGet, "/v1/players", gate("players:read", h.listPlayers))
	router.HandlerFunc(http.MethodGet, "/v1/players/:id", gate("players:read", h.showPlayer))
	router.HandlerFunc(http.MethodPost, "/v1/seasons/:id/players", gate("players:write", h.createPlayer))
	router.HandlerFunc(http.MethodPatch, "/v1/seasons/:id/players/:player_id", gate("players:write", h.updatePlayer))
	router.HandlerFunc(http.MethodDelete, "/v1/seasons/:id/players/:player_id", gate("players:write", h.deletePlayer))

	// Rides. The reads are gated with players:read and every command with
	// players:write — no ride code exists to name instead: migration 000004
	// seeds exactly eleven codes and the phase docs define no ride permission
	// vocabulary. The ride surface is the player domain's action surface (a
	// ride spends a named player's tokens), so it is gated with that domain's
	// codes, and players:write is the only write code the standard 'user' role
	// holds — gating the commands with it keeps an activated standard user
	// able to lock rides, the product's core flow, while an anonymous or
	// unactivated caller is refused before the handler runs. Every ride
	// command is idempotent (ADR-024) and runs through one shared handler that
	// takes the endpoint tag for the dedup identity; the gate wraps that
	// handler, order: gate then handler.
	router.HandlerFunc(http.MethodGet, "/v1/rides", gate("players:read", h.listRides))
	router.HandlerFunc(http.MethodGet, "/v1/rides/:id", gate("players:read", h.showRide))
	router.HandlerFunc(http.MethodPost, "/v1/rides", gate("players:write", h.createRide))
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/lock", gate("players:write", h.rideCommand("POST /v1/rides/:id/lock", h.svc.RideLock)))
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/burn", gate("players:write", h.rideCommand("POST /v1/rides/:id/burn", h.svc.RideBurn)))
	router.HandlerFunc(http.MethodPost, "/v1/rides/:id/unlock", gate("players:write", h.rideCommand("POST /v1/rides/:id/unlock", h.svc.RideUnlock)))
}
