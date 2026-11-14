package service

// The four ride use cases (create, lock, burn, unlock). Every one of them is
// idempotent (ADR-024) and runs in the RideLock shape — claim-first, one
// transaction:
//
//   - the key claim INSERT ... ON CONFLICT DO NOTHING runs before any read,
//     so a duplicate or in-flight request replays without executing;
//   - the claim, the calendar's round gates, the ride write and the stored
//     response share one transaction — any failure rolls the claim back, so
//     only successful executions are cached;
//   - the phase read is advisory (the round's phase window, read on the
//     calendar side); the write-instant facts are the port's two Tx guards,
//     which share-lock the calendar's rows inside this transaction;
//   - the marshal callback is the transport's presentation choice; its answer
//     is what a replay returns byte-identical.
//
// The clock is injected (Service.now): the phase gate reads the time through
// the seam instead of calling time.Now() inline.

import (
	"context"
	"errors"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
)

// RidePhase reads the ride's phase at the service's clock instant, through
// the calendar port. A round that is not open is the round refusal, raised
// before any phase exists.
func (s *Service) RidePhase(ctx context.Context, ride domain.Ride) (domain.Phase, error) {
	return s.schedule.PhaseForMatch(ctx, ride.MatchID, s.now())
}

// destinationOpenTx pins a continuation destination live at the write
// instant, through the port. Every refusal cause (unknown, ended, round
// closed) is the round-refusal — the Phase 04 contract for a stale pick,
// including the unknown-destination case (no FK hit needed).
func (s *Service) destinationOpenTx(ctx context.Context, tx pgxTx, matchID int) error {
	err := s.schedule.DestinationOpenTx(ctx, tx, matchID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrRoundNotOpen
	}
	return err
}

// BeginCommand opens the command's unit of work. The background context in
// the rollback is deliberate: the request may be canceled mid-tx, and the
// rollback must still run to release the connection.
func (s *Service) beginCommand(ctx context.Context) (pgxTx, error) {
	return s.transactor.Begin(ctx)
}

// RideCreate locks tokens on a chosen match: created -> locked, ActionPhase
// only (ADR-019: the initial-state contract is ride.Create's — caller-
// supplied state/acc/streak is discarded).
//
// The RidePhase read is the phase gate; it is advisory like every other
// ride-command read (ADR-024) — the round-status fact is re-checked at the
// write instant by the port's RoundOpenTx gate, whose FOR SHARE on the round
// row serializes the command against a concurrent close (ErrRoundNotOpen, the
// round-refusal 409, is the race lost).
func (s *Service) RideCreate(ctx context.Context, ride domain.Ride, token port.Token, marshal RideMarshal) (port.SavedResponse, error) {
	tx, err := s.beginCommand(ctx)
	if err != nil {
		return port.SavedResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	if err := ride.Create(phase); err != nil {
		return port.SavedResponse{}, err
	}

	// The round gate at the write instant: the port's FOR SHARE on the
	// ride's round row.
	if err := s.schedule.RoundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return port.SavedResponse{}, err
	}

	// The team reference is checked here since 000016 dropped
	// rides_team_id_fkey with the cross-context FKs): the port answers the
	// calendar fact, and an unknown team is the parent-gone 404 the FK
	// produced. Player existence stays DB-enforced (rides_player_id_fkey is
	// game-internal); the match reference is the round gate above.
	if _, err := s.schedule.Team(ctx, ride.TeamID); err != nil {
		return port.SavedResponse{}, err
	}

	ride, err = s.rides.InsertTx(ctx, tx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	// The lock intent (phase-05/06): the ride row was born carrying the
	// locked tokens (the lock and the birth are one statement today); the
	// ledger seam is in the flow so the Phase 06 balance swap changes no call
	// shape.
	if err := s.ledger.LockTokens(ctx, tx, ride.PlayerID, ride.TeamID, ride.ID, ride.TokensLocked, ride.BaseAtLock); err != nil {
		return port.SavedResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return port.SavedResponse{}, err
	}

	if err := s.idempotency.Save(ctx, tx, token, res); err != nil {
		return port.SavedResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return port.SavedResponse{}, err
	}

	return res, nil
}

// RideLock continues a won ride: won_pending -> locked onto the team's next
// match, DecisionPhase only.
//   - the advisory reads run on the pool beside the tx: per-statement
//     snapshots, error contract only — they prove nothing; one guarded
//     UPDATE is the whole write, its WHERE re-checks every contended fact
//     at the write instant, and 0 rows is the race lost (the round closed
//     under us) -> ErrRoundNotOpen
func (s *Service) RideLock(ctx context.Context, rideID int, token port.Token, marshal RideMarshal) (port.SavedResponse, error) {
	tx, err := s.beginCommand(ctx)
	if err != nil {
		return port.SavedResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.rides.Get(ctx, rideID)
	if err != nil {
		return port.SavedResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	// The continuation pick is load-bearing: NextForTeam chooses the
	// destination match, and the calendar's no-destination is the domain's
	// ErrNoNextMatch (a calendar conflict, not a missing ride).
	nextMatch, err := s.schedule.NextForTeam(ctx, ride.TeamID)
	if err != nil {
		return port.SavedResponse{}, err
	}

	origMatchID := ride.MatchID

	if err := ride.Lock(phase, nextMatch.ID); err != nil {
		return port.SavedResponse{}, err
	}

	// The write-instant round gates, through the port: the ride's own round
	// still open (the ride.Lock mutation happened on the in-memory copy — the
	// gate pins the original match's round), and the destination live. Both
	// take FOR SHARE locks inside this tx, so a concurrent close/resolve
	// serializes before the decision write — the same write-instant facts the
	// old in-statement EXISTS predicates checked.
	if err := s.schedule.RoundOpenTx(ctx, tx, origMatchID); err != nil {
		return port.SavedResponse{}, err
	}
	if err := s.destinationOpenTx(ctx, tx, nextMatch.ID); err != nil {
		return port.SavedResponse{}, err
	}

	ride, err = s.rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return port.SavedResponse{}, err
	}
	if err := s.idempotency.Save(ctx, tx, token, res); err != nil {
		return port.SavedResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return port.SavedResponse{}, err
	}

	return res, nil
}

// RideBurn ends a won ride: won_pending -> burned, tokens and accumulated
// bonus fulfilled as TB. DecisionPhase only; the RideLock execution shape
// (ADR-024) minus the continuation pick — the ride ends on its match, so the
// guarded write takes destination 0.
func (s *Service) RideBurn(ctx context.Context, rideID int, token port.Token, marshal RideMarshal) (port.SavedResponse, error) {
	tx, err := s.beginCommand(ctx)
	if err != nil {
		return port.SavedResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.rides.Get(ctx, rideID)
	if err != nil {
		return port.SavedResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	if err := ride.Burn(phase); err != nil {
		return port.SavedResponse{}, err
	}

	// The ride's own round gate at the write instant (no destination: Burn
	// ends the ride on its match).
	if err := s.schedule.RoundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return port.SavedResponse{}, err
	}

	ride, err = s.rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	// The payout intent (phase-05/06): the ride's locked tokens are
	// fulfilled as TB. There is no TB balance to write yet (Phase 06), and the
	// rides row keeps its money columns on a burned arrival — the intent is in
	// the flow so the Phase 06 swap changes no call shape.
	if err := s.ledger.Burn(ctx, tx, ride.ID, ride.TokensLocked); err != nil {
		return port.SavedResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return port.SavedResponse{}, err
	}
	if err := s.idempotency.Save(ctx, tx, token, res); err != nil {
		return port.SavedResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return port.SavedResponse{}, err
	}

	return res, nil
}

// RideUnlock ends a won ride: won_pending -> unlocked, accumulated bonus
// forfeited. DecisionPhase only; the RideLock execution shape (ADR-024) minus
// the continuation pick — the ride ends on its match, so the guarded write
// takes destination 0.
func (s *Service) RideUnlock(ctx context.Context, rideID int, token port.Token, marshal RideMarshal) (port.SavedResponse, error) {
	tx, err := s.beginCommand(ctx)
	if err != nil {
		return port.SavedResponse{}, err
	}
	// Background context: the request may be canceled mid-tx; the rollback
	// must still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	claimed, replay, err := s.idempotency.Claim(ctx, tx, token)
	if err != nil || !claimed {
		return replay, err
	}

	ride, err := s.rides.Get(ctx, rideID)
	if err != nil {
		return port.SavedResponse{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	if err := ride.Unlock(phase); err != nil {
		return port.SavedResponse{}, err
	}

	// The ride's own round gate at the write instant (no destination: Unlock
	// ends the ride on its match).
	if err := s.schedule.RoundOpenTx(ctx, tx, ride.MatchID); err != nil {
		return port.SavedResponse{}, err
	}

	// The forfeit intent BEFORE the arrival write: the unlocked arrival must
	// carry no acc (ride.Unlock's domain rule and the
	// rides_money_state_coupling CHECK agree), so the money release lands
	// first, inside the same tx. The ledger owns the write (phase-05/06); a
	// failed arrival write rolls the release back with it.
	if err := s.ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
		return port.SavedResponse{}, err
	}

	ride, err = s.rides.DecisionGuardedTx(ctx, tx, ride)
	if err != nil {
		return port.SavedResponse{}, err
	}

	res, err := rideResponse(ride, marshal)
	if err != nil {
		return port.SavedResponse{}, err
	}
	if err := s.idempotency.Save(ctx, tx, token, res); err != nil {
		return port.SavedResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return port.SavedResponse{}, err
	}

	return res, nil
}
