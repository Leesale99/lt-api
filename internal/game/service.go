// Package game owns the match/round lifecycle domain: the state machines
// for matches, rounds and seasons, their persistence, and the schema
// gates that keep Go-side validation and the SQL CHECK constraints in
// lockstep.
package game

import (
	"context"
	"errors"
	"time"

	"lt-api.aleksrdvn.com/internal/store"
)

var (
	ErrRoundNotOpen = errors.New("round is not open")
	ErrNoNextMatch  = errors.New("team has no upcoming match")
)

type Service struct {
	Store *Store
}

func NewService(store *Store) *Service {
	return &Service{
		Store: store,
	}
}

func (s *Service) RidePhase(ctx context.Context, ride Ride) (RoundPhase, error) {
	status, firstStartsAt, lastEndedAt, err := s.Store.Matches.PhaseForMatch(ctx, ride.MatchID)
	if err != nil {
		return "", err
	}

	if status != RoundOpen {
		return "", ErrRoundNotOpen
	}

	phase := Phase(time.Now(), firstStartsAt, lastEndedAt)

	return phase, nil
}

func (s *Service) RideCreate(ctx context.Context, ride Ride) (Ride, error) {
	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Create(phase)
	if err != nil {
		return Ride{}, err
	}

	return s.Store.Rides.Insert(ctx, ride)
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
func (s *Service) RideLock(ctx context.Context, rideID int, token IdempotencyToken, marshal RideMarshal) (IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return IdempotentResponse{}, err
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
		return IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	// The continuation pick is load-bearing: NextForTeam chooses the
	// destination match, and the schedule's not-found is the domain's
	// ErrNoNextMatch (a schedule conflict, not a missing ride).
	nextMatch, err := s.Store.Matches.NextForTeam(ctx, ride.TeamID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRecordNotFound):
			return IdempotentResponse{}, ErrNoNextMatch
		default:
			return IdempotentResponse{}, err
		}
	}

	if err := ride.Lock(phase, nextMatch.ID); err != nil {
		return IdempotentResponse{}, err
	}

	ride, err = s.Store.Rides.UpdateGuardedTx(ctx, tx, ride, nextMatch.ID)
	if err != nil {
		return IdempotentResponse{}, err
	}

	status, body, err := marshal(ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	res := IdempotentResponse{Status: status, Body: body}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return IdempotentResponse{}, err
	}

	return res, nil
}

// RideBurn ends a won ride: won_pending -> burned, tokens and accumulated
// bonus fulfilled as TB. DecisionPhase only; the RideLock execution shape
// (ADR-024) minus the continuation pick — the ride ends on its match, so
// the guarded write takes destination 0.
func (s *Service) RideBurn(ctx context.Context, rideID int, token IdempotencyToken, marshal RideMarshal) (IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return IdempotentResponse{}, err
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
		return IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	if err := ride.Burn(phase); err != nil {
		return IdempotentResponse{}, err
	}

	// 0 = no continuation destination: Burn ends the ride on its match.
	ride, err = s.Store.Rides.UpdateGuardedTx(ctx, tx, ride, 0)
	if err != nil {
		return IdempotentResponse{}, err
	}

	status, body, err := marshal(ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	res := IdempotentResponse{Status: status, Body: body}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return IdempotentResponse{}, err
	}

	return res, nil
}

// RideUnlock ends a won ride: won_pending -> unlocked, accumulated bonus
// forfeited. DecisionPhase only; the RideLock execution shape (ADR-024)
// minus the continuation pick — the ride ends on its match, so the guarded
// write takes destination 0.
func (s *Service) RideUnlock(ctx context.Context, rideID int, token IdempotencyToken, marshal RideMarshal) (IdempotentResponse, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return IdempotentResponse{}, err
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
		return IdempotentResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	if err := ride.Unlock(phase); err != nil {
		return IdempotentResponse{}, err
	}

	// 0 = no continuation destination: Unlock ends the ride on its match.
	ride, err = s.Store.Rides.UpdateGuardedTx(ctx, tx, ride, 0)
	if err != nil {
		return IdempotentResponse{}, err
	}

	status, body, err := marshal(ride)
	if err != nil {
		return IdempotentResponse{}, err
	}

	res := IdempotentResponse{Status: status, Body: body}
	if err := s.Store.Idempotency.Save(ctx, tx, token, res); err != nil {
		return IdempotentResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return IdempotentResponse{}, err
	}

	return res, nil
}
