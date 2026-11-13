package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"lt-api.aleksrdvn.com/internal/eventbus"
)

// ErrEndedAtInFuture reports a resolve whose ended_at lies at or after the
// current time. This is time policy, not request shape: it lives at the
// service door (ADR-026) so every ResolveMatch caller — not only the HTTP
// handler — passes the same rule. (It moved here from game in Phase C: the
// resolve entry point is schedule's script now.)
var ErrEndedAtInFuture = errors.New("ended_at must be in the past")

// Service is schedule's use-case layer: the transactions scripts that span
// a store write and an event publish (ResolveMatch), sitting on the same
// Store the CRUD handlers drive directly (ADR-028: schedule keeps the
// Transaction Script shape — no rich domain, just the two lifecycle
// exceptions that must publish). The embedded Store keeps the API layer's
// direct-store access unchanged.
//
// The bus is the process event exchange (internal/eventbus). NewService
// also hands it to the Store, whose RoundStore.Close publishes through it —
// the composition root wires one bus for the whole process.
type Service struct {
	*Store
	Bus *eventbus.Bus
	Log *slog.Logger
}

// NewService wires the schedule service onto its store and attaches the
// process bus to the store (Close publishes through it).
func NewService(store *Store, bus *eventbus.Bus, log *slog.Logger) *Service {
	store.attach(bus, log)
	return &Service{Store: store, Bus: bus, Log: log}
}

// ResolveMatch closes a match: the guarded score write (MatchStore.ResolveTx)
// in one transaction, then — after that commit — the MatchResultRecorded
// event that hands the result to the game context for ride settlement
// (ADR-027). Time policy is checked at the door (ADR-026), before any
// transaction: never open a tx just to fail cheaply, and never let a
// legality answer be outranked by a storage conflict (e.g. a closed match
// would turn this into a 409 if the check ran after ResolveTx).
//
// Publish is post-commit and synchronous: by the time ResolveMatch returns,
// the game handler has settled the locked rides of the match (or the
// settlement error has been logged as the accepted Phase C gap — the commit
// above is already durable, so a failed handler must not fail the request).
func (s *Service) ResolveMatch(ctx context.Context, matchID int, score Score, endedAt time.Time, version int) (Match, error) {
	if !endedAt.Before(time.Now()) {
		return Match{}, ErrEndedAtInFuture
	}

	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return Match{}, err
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	match, err := s.Store.Matches.ResolveTx(ctx, tx, matchID, score, endedAt, version)
	if err != nil {
		return Match{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Match{}, err
	}

	// Post-commit publish (ADR-027). The winner derivation is Match.Winner —
	// site 1 of the score→state rule; game's settlement derives ride
	// destinations from this payload and the rides_state_gate re-checks the
	// same facts against the committed match row, so publisher and
	// subscriber cannot disagree silently.
	event := MatchResultRecorded{
		EventID:      NewEventID(),
		MatchID:      match.ID,
		RoundID:      match.RoundID,
		SeasonID:     match.SeasonID,
		HomeTeamID:   match.HomeTeamID,
		AwayTeamID:   match.AwayTeamID,
		Score:        match.Score,
		Odds:         match.Odds,
		WinnerTeamID: match.Winner(),
		EndedAt:      endedAt,
	}
	s.publish(ctx, event)

	return match, nil
}

// publish delivers one event on the process bus, after the caller's
// transaction committed. Failure is the accepted Phase C crash gap: the
// commit is durable, so the event is logged and dropped — the Phase 07
// sweep (Phase 08 outbox) closes the hole. The context loses its
// cancellation: the publisher's request may be gone, but the handler's work
// is post-commit settlement, not a reply.
func (s *Service) publish(ctx context.Context, event any) {
	if s.Bus == nil {
		// Composition roots always attach a bus; a bare store in tests
		// publishes nothing.
		return
	}
	if err := s.Bus.Publish(context.WithoutCancel(ctx), event); err != nil && s.Log != nil {
		s.Log.Warn("event handler failed after commit; awaiting the Phase 07 sweep",
			"event_type", fmt.Sprintf("%T", event), "error", err)
	}
}
