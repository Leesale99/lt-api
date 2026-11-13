package game

// Event handlers (Phase C of the Game/Schedule split): game subscribes to
// schedule's MatchResultRecorded and RoundCompleted (published post-commit,
// in-process — internal/eventbus) and settles rides from their payloads.
//
// Handler shape, every one of them:
//
//   - idempotent: the EventID is claimed through internal/idempotency
//     BEFORE the handler's own transaction does any work — a replayed
//     event is a no-op success, and the Phase 08 outbox (or any future
//     delivery mechanism) can redeliver freely;
//   - one transaction: the claim, the ride transitions and the Ledger
//     money intents share it — settlement is atomic;
//   - payload-driven: ride outcomes are derived from the event's facts,
//     never from a join into schedule's tables. The DB stays the
//     enforcement home: rides_state_gate's result agreement (000011,
//     check 5) re-checks every locked→result arrival against the
//     committed match row, so a wrong payload cannot corrupt rides;
//   - no phase gates: settlement is system-initiated. The Go phase checks
//     (ride.WonPending's MatchPhase, ride.Unlock's DecisionPhase) are the
//     PLAYER commands' guards; the transitions themselves are the DB
//     transition graph's (rides_state_gate check 3), which these writes
//     go through like every other writer.
//
// Round-close is EVENTUAL since Phase C (documented at schedule's
// RoundStore.Close): Schedule closes on feed completion and publishes
// RoundCompleted; THIS side settles the round's rides after — undecided
// won_pending rides auto-unlock (rounds 1-37) or auto-burn (the season's
// last round, ADR-022). The old "block close while rides undecided" gate
// half was relaxed accordingly (migration 000014). The gap window — close
// committed, settlement not yet run, or settlement failed — is the
// accepted Phase C crash gap: the rides simply keep waiting (no money is
// lost), and the Phase 07 sweep closes it.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/idempotency"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// Event endpoints for the idempotency table's ops visibility.
const (
	matchResultEndpoint   = "event:schedule.MatchResultRecorded"
	roundCompleteEndpoint = "event:schedule.RoundCompleted"
)

// OnMatchResultRecorded settles the locked rides of one resolved match,
// from the payload: winner == ride.team → won_pending (the bonus accrues,
// the rule ride.WonPending encodes), otherwise lost (the accrued bonus is
// released — the Phase B set-based path skipped both money halves; this
// handler does them through the Ledger).
func (s *Service) OnMatchResultRecorded(ctx context.Context, event schedule.MatchResultRecorded) error {
	return s.runEvent(ctx, matchResultEndpoint, event.EventID, event, func(tx pgx.Tx) error {
		rides, err := s.Store.Rides.LockedForMatchTx(ctx, tx, event.MatchID)
		if err != nil {
			return err
		}

		for _, ride := range rides {
			if err := s.settleRideFromResult(ctx, tx, ride, event.HomeTeamID, event.AwayTeamID, event.WinnerTeamID, event.Odds); err != nil {
				return err
			}
		}
		return nil
	})
}

// OnRoundCompleted settles a closed round's rides, from the payload:
//
//  1. still-locked rides of the round's matches — settled per result, the
//     backstop for a MatchResultRecorded that never reached this process
//     (the accepted crash gap) or lost the resolve/settle race;
//  2. the undecided won_pending rides — force-resolved: unlocked on rounds
//     1-37 (the accrued bonus released), burned on the season's last round
//     (the chase option is unrepresentable once the season is over, the
//     tokens move to TB, the accrued bonus is kept — ADR-022).
//
// Statement order inside the transaction: locked settlements first, then
// the undecided sweep — a ride the backstop just settled to won_pending is
// swept by the same transaction, the old close-transaction semantics.
func (s *Service) OnRoundCompleted(ctx context.Context, event schedule.RoundCompleted) error {
	return s.runEvent(ctx, roundCompleteEndpoint, event.EventID, event, func(tx pgx.Tx) error {
		for _, result := range event.Results {
			rides, err := s.Store.Rides.LockedForMatchTx(ctx, tx, result.MatchID)
			if err != nil {
				return err
			}
			for _, ride := range rides {
				if err := s.settleRideFromResult(ctx, tx, ride, result.HomeTeamID, result.AwayTeamID, result.WinnerTeamID, result.Odds); err != nil {
					return err
				}
			}
		}

		matchIDs := make([]int, 0, len(event.Results))
		for _, result := range event.Results {
			matchIDs = append(matchIDs, result.MatchID)
		}

		undecided, err := s.Store.Rides.UndecidedForMatchesTx(ctx, tx, matchIDs)
		if err != nil {
			return err
		}

		for _, ride := range undecided {
			if event.IsFinal {
				// The force-burn arrival keeps its accrued bonus (the 000013
				// coupling deliberately excludes burn): the tokens move to
				// TB, the bonus rides along.
				if _, err := s.Store.Rides.ArriveTx(ctx, tx, ride.ID, RideWonPending, RideBurned, ride.Version); err != nil {
					return err
				}
				if err := s.Ledger.Burn(ctx, tx, ride.ID, ride.TokensLocked); err != nil {
					return err
				}
				continue
			}

			// The auto-unlock arrival carries no acc: release before the
			// state flip, the same order every unlocked arrival obeys.
			if err := s.Ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
				return err
			}
			if _, err := s.Store.Rides.ArriveTx(ctx, tx, ride.ID, RideWonPending, RideUnlocked, ride.Version); err != nil {
				return err
			}
		}
		return nil
	})
}

// settleRideFromResult is the payload→destination derivation for one
// locked ride (score→state rule site 2 of 3 — the pure Go copy; check 5 of
// rides_state_gate is the enforcement home, Match.Winner site 1, all tied
// by score_state_rule_test.go): won iff the result crowns the ride's team,
// lost otherwise (a draw crowns no winner). Then the transition and the
// money intents.
func (s *Service) settleRideFromResult(ctx context.Context, tx pgx.Tx, ride Ride, homeTeamID, awayTeamID int, winner *int, odds schedule.Odds) error {
	destination := RideLost
	if winner != nil && *winner == ride.TeamID {
		destination = RideWonPending
	}

	if destination == RideLost {
		// The lost arrival carries no acc: release before the state flip
		// (the money-state coupling, same order as RideUnlock).
		if err := s.Ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
			return err
		}
	}

	if _, err := s.Store.Rides.ArriveTx(ctx, tx, ride.ID, RideLocked, destination, ride.Version); err != nil {
		return err
	}

	if destination == RideWonPending {
		// The win accrues the bonus at the ride's team odds.
		matchOdds, err := teamOdds(ride.TeamID, homeTeamID, awayTeamID, odds)
		if err != nil {
			return err
		}
		if err := s.Ledger.AccrueBonus(ctx, tx, ride.ID, bonusDelta(matchOdds, ride.TokensLocked, ride.Streak)); err != nil {
			return err
		}
	}

	return nil
}

// teamOdds picks the ride's team side of the payload odds. A ride whose
// team is on neither side is corrupt data — refuse rather than guess.
func teamOdds(teamID, homeTeamID, awayTeamID int, odds schedule.Odds) (decimal.Decimal, error) {
	switch teamID {
	case homeTeamID:
		return decimal.NewFromFloat(odds.Home), nil
	case awayTeamID:
		return decimal.NewFromFloat(odds.Away), nil
	default:
		return decimal.Decimal{}, fmt.Errorf("ride team %d is on neither side of the match (%d vs %d)", teamID, homeTeamID, awayTeamID)
	}
}

// runEvent is the handlers' shared execution shape: claim the EventID
// first (inside the handler's transaction — a failed handler rolls its
// claim back, so an unprocessed event can be re-delivered; a replay is a
// no-op success), then the settlement work, then the stored response, one
// commit.
func (s *Service) runEvent(ctx context.Context, endpoint, eventID string, payload any, exec func(pgx.Tx) error) error {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	// Background context: the caller may be gone mid-tx; the rollback must
	// still run and release the connection.
	defer func() { _ = tx.Rollback(context.Background()) }()

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(encoded)

	token := idempotency.IdempotencyToken{Key: eventID, Endpoint: endpoint, Hash: hash[:]}
	claimed, _, err := s.Store.Idempotency.Claim(ctx, tx, token)
	if err != nil {
		return err
	}
	if !claimed {
		// Replay: the event already ran, byte-identical delivery is a
		// no-op success.
		return nil
	}

	if err := exec(tx); err != nil {
		return err
	}

	summary, err := json.Marshal(struct{ EventID string }{EventID: eventID})
	if err != nil {
		return err
	}
	if err := s.Store.Idempotency.Save(ctx, tx, token, idempotency.IdempotentResponse{Status: 200, Body: summary}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
