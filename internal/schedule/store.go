// Package schedule owns the league calendar: teams, seasons, rounds and
// matches — their entities, validators and stores — plus the pure phase
// derivation from real-world match times. The schedule's writes are the
// season/round/match state machines; their advisory Go-side validators and
// the authoritative freeze gates (ADR-008) keep the two halves in
// lockstep.
//
// Package layout is the ADR-028 shape (Transaction Script + Layered-3):
// anemic entities + validators here, straightforward store SQL, and the
// Service for the use cases that must publish (ResolveMatch; Close's
// RoundCompleted). Since Phase C of the Game/Schedule split the
// match-result handoff to game is an event (internal/schedule/events.go),
// not a shared transaction — game consumes schedule lookups through a
// narrow port (game.SchedulePort) and schedule never imports game
// (game → schedule is the only legal import direction).
package schedule

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/eventbus"
)

// Store composes the schedule's per-entity stores. It is the single
// dependency the API layer holds for calendar CRUD; game consumes it
// structurally as its SchedulePort. Generic storage primitives (sentinel
// errors, Postgres error classifiers, pagination) live in internal/store,
// shared with the other contexts.
//
// Pool rides on Store so a use-case method can own a transaction that spans
// several stores (RoundStore.Open and Close are the consumers).
//
// bus/log carry the process event exchange and its logger into
// RoundStore.Close's post-commit publish; both are nil-safe (a bare store
// publishes nothing) and set by NewService, the composition point.
type Store struct {
	Pool    *pgxpool.Pool
	Seasons SeasonStore
	Rounds  RoundStore
	Teams   TeamStore
	Matches MatchStore

	bus *eventbus.Bus
	log *slog.Logger
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:    pool,
		Seasons: SeasonStore{pool},
		Rounds:  RoundStore{pool: pool},
		Teams:   TeamStore{pool},
		Matches: MatchStore{pool},
	}
}

// NewService's composition point: the process bus and logger also reach
// RoundStore.Close, whose post-commit publish runs inside the store layer.
func (s *Store) attach(bus *eventbus.Bus, log *slog.Logger) {
	s.bus = bus
	s.log = log
	s.Rounds.bus = bus
	s.Rounds.log = log
}
