// Package service holds the schedule context's use cases — the layer between
// the HTTP handlers and the repositories. Plain CRUD passes through to a
// repository; the use cases that carry a rule or a side effect live here:
// the round lifecycle dispatch (round_lifecycle.go), the resolve script that
// commits a result and publishes MatchResultRecorded (resolve_match.go), and
// the delete policies that consult the game context before removing a
// referenced record (delete_policy.go).
//
// Publishing belongs to this layer, not the repositories: the service owns
// the transaction-to-event ordering (post-commit, in-process, synchronous)
// and the only bus/log the process wires.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
)

// Repositories is the storage set the service drives. The composition root
// builds them once (the game-facing surface needs the same instances) and
// hands them over here.
type Repositories struct {
	Seasons *repository.SeasonRepository
	Rounds  *repository.RoundRepository
	Teams   *repository.TeamRepository
	Matches *repository.MatchRepository
}

// Service is schedule's use-case layer: the per-entity use-case objects the
// handlers drive, plus the use cases that span entities (ResolveMatch,
// UpdateRound, the delete policies).
//
// The pool exists for exactly one reason: ResolveMatch owns a transaction
// boundary. The bus and logger are the process event exchange and its
// logger (internal/eventbus) — nil-safe, so a bare service in a unit test
// publishes nothing.
type Service struct {
	Seasons *SeasonService
	Rounds  *RoundService
	Teams   *TeamService
	Matches *MatchService

	teams   *repository.TeamRepository
	matches *repository.MatchRepository
	seasons *repository.SeasonRepository

	pool *pgxpool.Pool
	bus  *eventbus.Bus
	log  *slog.Logger

	// usage is the game context's answer to "is this record still
	// referenced?" — the consumer-side port the delete policies call. It is
	// wired by the composition root (UseGameRefs) because schedule must not
	// import game.
	usage GameUsage

	// now is the resolve script's clock. Production leaves it at time.Now;
	// it is a field so a test can pin the time-policy boundary without
	// sleeping.
	now func() time.Time
}

// New wires the use cases onto the composition root's repositories, bus and
// logger.
func New(pool *pgxpool.Pool, repos Repositories, bus *eventbus.Bus, log *slog.Logger) *Service {
	s := &Service{
		Seasons: NewSeasonService(repos.Seasons),
		Rounds:  NewRoundService(repos.Rounds, repos.Matches, bus, log),
		Teams:   NewTeamService(repos.Teams),
		Matches: NewMatchService(repos.Matches),

		teams:   repos.Teams,
		matches: repos.Matches,
		seasons: repos.Seasons,

		pool: pool,
		bus:  bus,
		log:  log,
		now:  time.Now,
	}

	return s
}

// UseGameRefs installs the game context's reference port the delete policies
// consult (delete_policy.go). The composition root adapts the game store to
// it until game exposes a facade of its own; schedule never imports game.
func (s *Service) UseGameRefs(usage GameUsage) {
	s.usage = usage
}

// publish delivers one event on the process bus, after the caller's
// transaction committed. Failure is the accepted crash gap: the
// commit is durable, so the event is logged and dropped — the Phase 07
// sweep (Phase 08 outbox) closes the hole. The context loses its
// cancellation: the publisher's request may be gone, but the handler's work
// is post-commit settlement, not a reply.
func publish(ctx context.Context, bus *eventbus.Bus, log *slog.Logger, event any) {
	if bus == nil {
		// Composition roots always attach a bus; a bare service in tests
		// publishes nothing.
		return
	}
	if err := bus.Publish(context.WithoutCancel(ctx), event); err != nil && log != nil {
		log.Warn("event handler failed after commit; awaiting the Phase 07 sweep",
			"event_type", fmt.Sprintf("%T", event), "error", err)
	}
}
