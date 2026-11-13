// Package game owns the ride gameplay domain: the ride state machines and
// their commands, the persistence of rides and players, and the event
// handlers that settle rides from schedule's MatchResultRecorded/
// RoundCompleted payloads (see store.go for the split notes).
package game

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/idempotency"
	"lt-api.aleksrdvn.com/internal/ledger"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/store"
)

var (
	ErrRoundNotOpen = errors.New("round is not open")
	ErrNoNextMatch  = errors.New("team has no upcoming match")
	// ErrInvalidRoundPhase rejects a ride command issued in the wrong round
	// phase (e.g. Burn during MatchPhase). Distinct from ErrInvalidTransition
	// so handlers can map the two to different client-facing errors. The
	// phase itself is schedule knowledge (schedule.RoundPhase); which command
	// is legal in which phase is the ride domain's rule, so the error lives
	// here.
	ErrInvalidRoundPhase = errors.New("command not allowed in this round phase")
)

// (ErrEndedAtInFuture moved to schedule in Phase C, with the resolve
// script it belongs to — the api maps it to the same 422 from there.)

// Service is the ride domain's entry point. It owns the game context's
// stores, consumes the schedule context through the SchedulePort (the
// calendar facts its commands need: phase window, round-open guards,
// next-match pick — the resolve score write is schedule's script since
// Phase C), and the Ledger for every money mutation (game sends intents;
// the ledger owns the writes — phase-05/06). The event handlers settle
// rides from schedule's MatchResultRecorded/RoundCompleted payloads.
// game → schedule and game → ledger are the only legal import directions
// (ADR-028).
type Service struct {
	Store    *Store
	Schedule SchedulePort
	Ledger   ledger.Ledger
}

func NewService(store *Store, sched SchedulePort, led ledger.Ledger) *Service {
	return &Service{
		Store:    store,
		Schedule: sched,
		Ledger:   led,
	}
}

// roundOpenTx pins the ride's own round OPEN at the write instant, inside
// the command's transaction, through the port (the FOR SHARE gate). The
// error mapping is the Phase 04 contract: unknown match →
// ErrRecordNotFound, found-but-not-open → ErrRoundNotOpen (the
// round-refusal 409).
func (s *Service) roundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	status, err := s.Schedule.RoundOpenTx(ctx, tx, matchID)
	switch {
	case errors.Is(err, store.ErrRecordNotFound):
		return store.ErrRecordNotFound
	case err != nil:
		return err
	case status != schedule.RoundOpen:
		return ErrRoundNotOpen
	default:
		return nil
	}
}

// destinationOpenTx pins a continuation destination LIVE at the write
// instant, through the port. Every refusal cause (unknown, ended, round
// closed) is the round-refusal 409 — the Phase 04 contract for a stale
// pick, including the unknown-destination case (no FK hit needed).
func (s *Service) destinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	err := s.Schedule.DestinationOpenTx(ctx, tx, matchID)
	if errors.Is(err, store.ErrRecordNotFound) {
		return ErrRoundNotOpen
	}
	return err
}

// RideMarshal is the presentation callback a ride command hands its result
// to: it returns the status and marshaled body the command stores as its
// replayable response (ADR-024). It is game-local (the stored answer is a
// ride); the persistence of that answer is idempotency's.
type RideMarshal func(Ride) (int, []byte, error)

// rideResponse runs the command's presentation callback (ADR-024): its
// answer is exactly what gets stored and what a replay returns byte-
// identical — the handler owns status and body shape, the service never
// re-serializes on top of it.
func rideResponse(ride Ride, marshal RideMarshal) (idempotency.IdempotentResponse, error) {
	status, body, err := marshal(ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	return idempotency.IdempotentResponse{Status: status, Body: body}, nil
}

func (s *Service) RidePhase(ctx context.Context, ride Ride) (schedule.RoundPhase, error) {
	status, firstStartsAt, lastEndedAt, err := s.Schedule.PhaseForMatch(ctx, ride.MatchID)
	if err != nil {
		return "", err
	}

	if status != schedule.RoundOpen {
		return "", ErrRoundNotOpen
	}

	phase := schedule.Phase(time.Now(), firstStartsAt, lastEndedAt)

	return phase, nil
}

// RideCreate locks tokens on a chosen match: created -> locked, ActionPhase
// only (ADR-019: the initial-state contract is ride.Create's — caller-supplied
// state/acc/streak is discarded). The RidePhase read is the phase gate; it is
// advisory like every other ride-command read (ADR-024) — the round-status
// fact is re-checked at the write instant by the port's RoundOpenTx gate,
// whose FOR SHARE on the round row serializes the command against a
// concurrent close (ErrRoundNotOpen, the round-refusal 409, is the race
// lost).
//
// Every ride command is idempotent (ADR-024), so create executes in the
// RideLock shape (claim-first, one tx): the key claim, the round gate, the
// insert and the stored response share one transaction — any failure rolls
// the claim back (failures are never cached), and a duplicate replays the
// stored response byte-identical instead of creating a second ride.
// The handler validates the ride before the call, so a 422 never claims.
func (s *Service) RideCreate(ctx context.Context, ride Ride, token idempotency.IdempotencyToken, marshal RideMarshal) (idempotency.IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.Store.Idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := ride.Create(phase); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The round gate at the write instant: the port's FOR SHARE on the
	// ride's round row (see RideCreate's doc — the serialization the old
	// in-statement guard provided, on schedule's side of the seam).
	if err := s.roundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The team reference is checked here since Phase D (000016 dropped
	// rides_team_id_fkey with the cross-context FKs): the port answers the
	// calendar fact, and an unknown team is the parent-gone 404 the FK
	// produced. Player existence stays DB-enforced (rides_player_id_fkey
	// is game-internal); the match reference is the round gate above.
	if _, err := s.Schedule.Team(ctx, ride.TeamID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	ride, err = s.Store.Rides.InsertTx(ctx, tx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The lock intent (phase-05/06): the ride row was born carrying the
	// locked tokens (see ledger.Store for why the lock and the birth are
	// one statement today); the ledger seam is in the flow so the Phase 06
	// balance swap changes no call shape.
	if err := s.Ledger.LockTokens(ctx, tx, ride.PlayerID, ride.TeamID, ride.ID, ride.TokensLocked, ride.BaseAtLock); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	return res, nil
}

// RideLock continues a won ride: won_pending -> locked onto the team's next
// match, DecisionPhase only. Every ride command is idempotent (ADR-024), and
// this function is the whole execution shape:
//   - claim-first: the key INSERT ... ON CONFLICT DO NOTHING runs before any
//     read, so a duplicate or in-flight request replays without executing
//   - claim, guarded write and stored response share one transaction — any
//     failure rolls the claim back, so only successful executions are cached
//   - the advisory reads run on the pool beside the tx: per-statement
//     snapshots, error contract only — they prove nothing; one guarded
//     UPDATE is the whole write, its WHERE re-checks every contended fact
//     at the write instant, and 0 rows is the race lost (the round closed
//     under us) -> ErrRoundNotOpen
//   - the marshal callback is the handler's presentation choice; its answer
//     is what a replay returns byte-identical
func (s *Service) RideLock(ctx context.Context, rideID int, token idempotency.IdempotencyToken, marshal RideMarshal) (idempotency.IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.Store.Idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The continuation pick is load-bearing: NextForTeam chooses the
	// destination match, and the schedule's not-found is the domain's
	// ErrNoNextMatch (a schedule conflict, not a missing ride).
	nextMatch, err := s.Schedule.NextForTeam(ctx, ride.TeamID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRecordNotFound):
			return idempotency.IdempotentResponse{}, ErrNoNextMatch
		default:
			return idempotency.IdempotentResponse{}, err
		}
	}

	origMatchID := ride.MatchID

	if err := ride.Lock(phase, nextMatch.ID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The write-instant round gates, through the port: the ride's own round
	// still open (the ride.Lock mutation happened on the in-memory copy —
	// the gate pins the original match's round), and the destination
	// live. Both take FOR SHARE locks inside this tx, so a concurrent
	// close/resolve serializes before the decision write — the same
	// write-instant facts the old in-statement EXISTS predicates checked.
	if err := s.roundOpenTx(ctx, tx, origMatchID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	if err := s.destinationOpenTx(ctx, tx, nextMatch.ID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	ride, err = s.Store.Rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	return res, nil
}

// RideBurn ends a won ride: won_pending -> burned, tokens and accumulated
// bonus fulfilled as TB. DecisionPhase only; the RideLock execution shape
// (ADR-024) minus the continuation pick — the ride ends on its match, so
// the guarded write takes destination 0.
func (s *Service) RideBurn(ctx context.Context, rideID int, token idempotency.IdempotencyToken, marshal RideMarshal) (idempotency.IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.Store.Idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := ride.Burn(phase); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The ride's own round gate at the write instant (no destination:
	// Burn ends the ride on its match).
	if err := s.roundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	ride, err = s.Store.Rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The payout intent (phase-05/06): the ride's locked tokens are
	// fulfilled as TB. There is no TB balance to write yet (Phase 06), and
	// the rides row keeps its money columns on a burned arrival — the
	// intent is in the flow so the Phase 06 swap changes no call shape.
	if err := s.Ledger.Burn(ctx, tx, ride.ID, ride.TokensLocked); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	return res, nil
}

// RideUnlock ends a won ride: won_pending -> unlocked, accumulated bonus
// forfeited. DecisionPhase only; the RideLock execution shape (ADR-024)
// minus the continuation pick — the ride ends on its match, so the guarded
// write takes destination 0.
func (s *Service) RideUnlock(ctx context.Context, rideID int, token idempotency.IdempotencyToken, marshal RideMarshal) (idempotency.IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.Store.Idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := ride.Unlock(phase); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The ride's own round gate at the write instant (no destination:
	// Unlock ends the ride on its match).
	if err := s.roundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	// The forfeit intent BEFORE the arrival write: the unlocked arrival
	// must carry no acc (ride.Unlock's domain rule and the
	// rides_money_state_coupling CHECK agree), so the money release lands
	// first, inside the same tx. The ledger owns the write (phase-05/06);
	// a failed arrival write rolls the release back with it.
	if err := s.Ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	ride, err = s.Store.Rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return idempotency.IdempotentResponse{}, err
	}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return idempotency.IdempotentResponse{}, err
	}

	return res, nil
}
