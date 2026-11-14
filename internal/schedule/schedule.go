// Package schedule owns the league calendar: teams, seasons, rounds and
// matches — their entities, validators and repositories — plus the pure
// phase derivation from real-world match times. The schedule's writes are
// the season/round/match state machines; their advisory Go-side validators
// and the authoritative freeze gates (ADR-008) keep the two halves in
// lockstep.
//
// The package is the module's public facade (ADR-028, layered shape): this
// file is the only thing outside the module may import, and it exposes the
// game-facing calendar reads (consumed through game's anti-corruption layer),
// the use-case surface the api layer drives, the event payloads, and its route
// table. Everything else lives under internal/ — model, repository, service
// and handler — so the layers cannot leak across the boundary.
//
// The Game/Schedule split made the match-result handoff to game an event
// (internal/model/events.go), not a shared transaction: game consumes
// schedule lookups through its narrow port and schedule never imports game
// (game → schedule is the only legal import direction).
package schedule

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/schedule/internal/handler"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
	"lt-api.aleksrdvn.com/internal/schedule/internal/service"
)

// Module is the schedule bounded context as the rest of the process sees it:
// the use cases (its service layer), the HTTP surface, and the calendar
// facts game's ride commands read. The composition root builds exactly one
// per process and hands the same value to the api gateway and to game.
type Module struct {
	// The entity use-case surfaces. Exported because the module's own
	// callers legitimately need them by name: game's concurrency tests drive
	// Rounds.Close directly (phase 4 replaces that with a seam), and the
	// handlers are thin translations on top of the same objects.
	Seasons *service.SeasonService
	Rounds  *service.RoundService
	Teams   *service.TeamService
	Matches *service.MatchService

	svc     *service.Service
	handler *handler.Handler
	port    *repository.GameFacing
	pool    *pgxpool.Pool
}

// NewModule wires the schedule module: the repositories over the schedule
// pool, the use cases over them, and the HTTP handlers over the use cases.
// bus/log are the process event exchange and its logger — the module
// publishes MatchResultRecorded and RoundCompleted through them, post-commit
// (ADR-027). Both are nil-safe: a module without a bus publishes nothing.
func NewModule(pool *pgxpool.Pool, bus *eventbus.Bus, log *slog.Logger) *Module {
	repos := service.Repositories{
		Seasons: repository.NewSeasonRepository(pool),
		Rounds:  repository.NewRoundRepository(pool),
		Teams:   repository.NewTeamRepository(pool),
		Matches: repository.NewMatchRepository(pool),
	}

	svc := service.New(pool, repos, bus, log)

	return &Module{
		Seasons: svc.Seasons,
		Rounds:  svc.Rounds,
		Teams:   svc.Teams,
		Matches: svc.Matches,

		svc:     svc,
		handler: handler.New(svc, log),
		port:    repository.NewGameFacing(repos.Matches, repos.Teams),
		pool:    pool,
	}
}

// UseGameRefs installs the game context's reference port the delete policies
// consult (a team or match still referenced by players/rides must not be
// deleted). The composition root adapts game's store to service.GameUsage;
// declaring the port at the consumer keeps schedule free of a game import.
func (m *Module) UseGameRefs(usage service.GameUsage) {
	m.svc.UseGameRefs(usage)
}

// RegisterRoutes installs the module's routes on the gateway's router. Paths,
// methods and the permission gate are the pre-refactor api/routes.go's,
// unchanged: the module owns its wire surface, the gateway owns the router
// and the middleware chain around it.
func (m *Module) RegisterRoutes(router handler.RouteRegistrar, gate handler.PermissionGate) {
	m.handler.RegisterRoutes(router, gate)
}

// The game-facing reads: game's schedule port is satisfied through game's own
// anti-corruption layer (game/internal/adapter/outbound/scheduleacl.go), which
// wraps this surface and translates its types and errors into game's. The SQL
// behind these reads is schedule's own, executed on the caller's transaction
// where noted.

// PhaseForMatch returns the ride-phase inputs for the round the given match
// belongs to: the round status (the ErrRoundNotOpen gate input) plus the
// phase window — min(starts_at), max(ended_at) over the round's matches.
func (m *Module) PhaseForMatch(ctx context.Context, matchID int) (RoundStatus, *time.Time, *time.Time, error) {
	return m.port.PhaseForMatch(ctx, matchID)
}

// NextForTeam returns the team's next live match — the continuation pick
// behind RideLock.
func (m *Module) NextForTeam(ctx context.Context, teamID int) (Match, error) {
	return m.port.NextForTeam(ctx, teamID)
}

// RoundOpenTx pins the ride's own round OPEN at the write instant inside the
// caller's transaction; ErrRecordNotFound when the match is unknown.
func (m *Module) RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) (RoundStatus, error) {
	return m.port.RoundOpenTx(ctx, tx, matchID)
}

// DestinationOpenTx pins a continuation destination LIVE at the write instant
// inside the caller's transaction. Every refusal (unknown, ended, round
// closed) is persistence.ErrRecordNotFound — the caller maps it to the
// round-refusal.
func (m *Module) DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	return m.port.DestinationOpenTx(ctx, tx, matchID)
}

// Match returns the committed facts of one match — the input of the
// result-agreement check the settlement handlers run.
func (m *Module) Match(ctx context.Context, matchID int) (Match, error) {
	return m.port.Match(ctx, matchID)
}

// Team reports one team's existence — the read behind ride create's team
// reference and the player handlers' favourite-team check.
// ErrRecordNotFound when the team is unknown.
func (m *Module) Team(ctx context.Context, teamID int) (Team, error) {
	return m.port.Team(ctx, teamID)
}

// Season reports one season's facts — the read other gateway handlers need
// for their own season references (player/match creation). ErrRecordNotFound
// when the season is unknown.
func (m *Module) Season(ctx context.Context, seasonID int) (Season, error) {
	return m.Seasons.Get(ctx, seasonID)
}

// ResolveMatch is the resolve use case (service.ResolveMatch): the guarded
// score write in one transaction, then the MatchResultRecorded publish —
// the module's public entry point to schedule's script.
func (m *Module) ResolveMatch(ctx context.Context, matchID int, score Score, endedAt time.Time, version int) (Match, error) {
	return m.svc.ResolveMatch(ctx, matchID, score, endedAt, version)
}

// Type aliases: the module's public vocabulary is the model's, re-exported so
// callers (game's port, the api gateway, the event subscribers) keep naming
// the schedule types they always named. Aliases, not copies: there is one
// definition of every fact.
type (
	// Match and its parts.
	Match       = model.Match
	MatchStatus = model.MatchStatus
	Odds        = model.Odds
	Score       = model.Score

	// Round, its statuses and the phase derivation.
	Round       = model.Round
	RoundStatus = model.RoundStatus
	RoundPhase  = model.RoundPhase

	// Season and team.
	Season       = model.Season
	SeasonStatus = model.SeasonStatus
	Team         = model.Team

	// The event payloads (schedule's public contract, ADR-027).
	MatchResult         = model.MatchResult
	MatchResultRecorded = model.MatchResultRecorded
	RoundCompleted      = model.RoundCompleted
)

const (
	// Match statuses.
	MatchCreated    = model.MatchCreated
	MatchInProgress = model.MatchInProgress
	MatchPostponed  = model.MatchPostponed
	MatchClosed     = model.MatchClosed

	// Round statuses.
	RoundCreated = model.RoundCreated
	RoundOpen    = model.RoundOpen
	RoundClosed  = model.RoundClosed

	// Season statuses.
	SeasonCreated    = model.SeasonCreated
	SeasonOpen       = model.SeasonOpen
	SeasonInProgress = model.SeasonInProgress
	SeasonClosed     = model.SeasonClosed

	// Round phases.
	ActionPhase   = model.ActionPhase
	MatchPhase    = model.MatchPhase
	DecisionPhase = model.DecisionPhase

	// RoundsPerSeason is the calendar's round count (double round-robin).
	RoundsPerSeason = model.RoundsPerSeason
)

// ErrEndedAtInFuture is the resolve use case's time-policy refusal, raised
// before any transaction (ADR-026).
var ErrEndedAtInFuture = model.ErrEndedAtInFuture

// Phase derives the gameplay phase of a round from real-world facts (see
// model.Phase).
func Phase(now time.Time, firstStartsAt, lastEndedAt *time.Time) RoundPhase {
	return model.Phase(now, firstStartsAt, lastEndedAt)
}

// NewEventID generates an event's dedup identity (see model.NewEventID).
func NewEventID() string {
	return model.NewEventID()
}
