// Package game is the ride gameplay bounded context, exposed as a hexagonal
// module (ADR-028): this file is the module's public facade and the only
// thing outside the module may import. It exposes the composition-root
// constructor, the route table, the wire DTO vocabulary and the
// reference capabilities the calendar's delete policies consult.
//
// Everything else lives under internal/:
//
//	internal/core/domain    the ride state machine, players, phases, sentinels
//	internal/core/port      the consumer-side interfaces the core drives
//	internal/core/service   the use cases (ride commands, settlement, players)
//	internal/adapter/inbound/http   the frozen HTTP wire format
//	internal/adapter/outbound       the calendar ACL and the event ACL
//	internal/adapter/outbound/postgres  the SQL behind the ports
//
// The core imports no other bounded context. The calendar reaches the module
// through adapter/outbound's anti-corruption layer only — that adapter is the
// one game package that imports the schedule facade — and the calendar's
// events are translated into game-owned commands at the same boundary. Money
// movements go to the ledger, which the composition root hands in as game's
// own port (structural satisfaction, no adapter). game → schedule and
// game → ledger are the only legal cross-context directions (ADR-028).
package game

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	gamehttp "lt-api.aleksrdvn.com/internal/game/internal/adapter/inbound/http"
	"lt-api.aleksrdvn.com/internal/game/internal/adapter/outbound"
	"lt-api.aleksrdvn.com/internal/game/internal/adapter/outbound/postgres"
	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/game/internal/core/service"
	"lt-api.aleksrdvn.com/internal/kernel/eventbus"
	"lt-api.aleksrdvn.com/internal/kernel/idempotency"
)

// Module is the game bounded context as the rest of the process sees it. The
// composition root builds exactly one per process and hands the same value to
// the api gateway (routes) and to the schedule module (reference capabilities).
type Module struct {
	svc     *service.Service
	handler *gamehttp.Handler
}

// ScheduleAPI is the calendar facade surface the module consumes — the
// structural subset of *schedule.Module the anti-corruption layer wraps.
// Re-exported so the composition root can hand the module over without
// importing a game-internal package.
type ScheduleAPI = outbound.ScheduleAPI

// The route table's vocabulary, re-exported for the gateway.
type (
	PermissionGate = gamehttp.PermissionGate
	RouteRegistrar = gamehttp.RouteRegistrar
	UserDirectory  = gamehttp.UserDirectory
)

// RideDTO is a ride as the wire exposes it — the frozen HTTP shape (also the
// body stored as an idempotent response). Exported as the module's public
// vocabulary; the transport is its only producer.
type RideDTO = gamehttp.RideResponse

// NewModule wires the game module: the Postgres adapters over the game pool,
// the calendar anti-corruption layer over the schedule facade, the ledger and
// the idempotency engine as game's own ports, and the use cases over all of
// them. bus/log are the process event exchange and its logger: the module
// subscribes its settlement handlers to the calendar's events through the
// event ACL (post-commit delivery, unchanged ordering). Both are nil-safe: a
// module without a bus hears no events.
func NewModule(pool *pgxpool.Pool, scheduleAPI ScheduleAPI, ledgerAPI port.Ledger, idem idempotency.Repository, bus *eventbus.Bus, log *slog.Logger) *Module {
	svc := service.New(service.Deps{
		Rides:       postgres.NewRideRepository(pool),
		Players:     postgres.NewPlayerRepository(pool),
		Schedule:    outbound.NewScheduleACL(scheduleAPI),
		Ledger:      ledgerAPI,
		Idempotency: outbound.NewIdempotency(idem),
		Transactor:  postgres.NewTransactor(pool),
	})

	module := &Module{
		svc:     svc,
		handler: gamehttp.New(svc, log),
	}

	if bus != nil {
		outbound.Subscribe(bus, svc)
	}

	return module
}

// UseIdentity installs the identity read the player-registration transport
// performs (the authenticated principal's user row must exist before game
// writes its reference). The composition root installs it next to the module
// it already builds; the module never imports identity itself.
func (m *Module) UseIdentity(users UserDirectory) {
	m.handler.UseIdentity(users)
}

// RegisterRoutes installs the module's routes on the gateway's router. Paths,
// methods and the permission gate are the pre-refactor api/routes.go's,
// unchanged: the module owns its wire surface, the gateway owns the router and
// the middleware chain around it.
func (m *Module) RegisterRoutes(router RouteRegistrar, gate PermissionGate) {
	m.handler.RegisterRoutes(router, gate)
}

// The reference capabilities the calendar's delete policies consult. They are
// the game context's answers about its own rows; the calendar declares the
// port (schedule's service.GameUsage), game owns the facts, and nobody imports
// across the wrong way (ADR-028).
//
// The delete-side capability speaks GAME's vocabulary — ErrRecordInUse below,
// re-exported from the core domain — never the kernel persistence package's.
// The calendar's port speaks its own model.ErrRecordInUse, so the composition
// root adapts the two at the boundary (cmd/api's gameRefs); neither module
// imports the other's sentinels.

// PlayersExistForTeam reports whether any player favours the team
// (players_favorite_team_id_fkey's old RESTRICT half).
func (m *Module) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return m.svc.PlayersExistForTeam(ctx, teamID)
}

// RidesExistForTeam reports whether any ride rides on the team
// (rides_team_id_fkey's old RESTRICT half).
func (m *Module) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return m.svc.RidesExistForTeam(ctx, teamID)
}

// RidesExistForMatch reports whether any ride rides on the match
// (rides_match_id_fkey's old RESTRICT half).
func (m *Module) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return m.svc.RidesExistForMatch(ctx, matchID)
}

// DeletePlayersForSeasonTx deletes every player of the season
// (players_season_id_fkey's old CASCADE half) on the calendar's transaction:
// the season row and its registrations are one atomic operation again, the
// shape the pre-000016 cascade had. The statement is game's SECURITY DEFINER
// function (migration 000021) — schedule's pool has no privilege on
// game.players, so game's SQL runs as the schema owner inside schedule's tx.
//
// A ride still referencing one of the players keeps the old cascade's
// refusal: ErrRecordInUse (game's), which the composition adapter turns into
// the calendar's own in-use sentinel — the same 409 the blocked cascade
// produced.
func (m *Module) DeletePlayersForSeasonTx(ctx context.Context, tx pgx.Tx, seasonID int) error {
	return m.svc.DeletePlayersForSeasonTx(ctx, tx, seasonID)
}

// The core's sentinels, re-exported for the composition boundary: the
// calendar's port adapter compares against these instead of reaching into the
// module's internals.
var (
	ErrRecordInUse = domain.ErrRecordInUse
	ErrNotFound    = domain.ErrNotFound
)
