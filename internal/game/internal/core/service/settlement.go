package service

// Settlement (the Game/Schedule split's event seam): game consumes the
// calendar's MatchResultRecorded and RoundCompleted events — published
// post-commit, in-process (internal/kernel/eventbus) — and settles rides from their
// payloads.
//
// The event types below are game's own command vocabulary. The calendar's
// payload types remain the calendar's public contract; the anti-corruption
// layer at adapter/outbound translates one into the other before the event
// enters the core, so no schedule type crosses into this package.
//
// Handler shape, every one of them:
//
//   - idempotent: the EventID is claimed through the idempotency port BEFORE
//     the handler's own transaction does any work — a replayed event is a
//     no-op success, and a future outbox (phase 08) or any other delivery
//     mechanism can redeliver freely;
//   - one transaction: the claim, the ride transitions and the ledger money
//     intents share it — settlement is atomic;
//   - payload-driven: ride outcomes are derived from the event's facts, never
//     from a join into the calendar's tables. The committed match is still the
//     source of truth: the result agreement check below re-reads it through
//     the port, so a wrong payload cannot corrupt rides — not its destination
//     and not the rate its bonus is paid at (the committed snapshot carries
//     both).
//   - no phase gates: settlement is system-initiated. The Go phase checks
//     (ride.WonPending's MatchPhase, ride.Unlock's DecisionPhase) are the
//     PLAYER commands' guards; the transitions themselves are the DB
//     transition graph's, which these writes go through like every other
//     writer.
//
// Round-close is EVENTUAL (documented at the calendar's round close):
// the calendar closes on feed completion and publishes RoundCompleted; THIS
// side settles the round's rides after — undecided won_pending rides
// auto-unlock (rounds 1-37) or auto-burn (the season's last round, ADR-022).
// The gap window — close committed, settlement not yet run, or settlement
// failed — is the accepted crash gap: the rides simply keep waiting (no money
// is lost), and the phase 07 sweep closes it.
//
// The two settlement transactions serialize on the settled round (the
// transactor's advisory lock): a resolve and a close publishing at the same
// moment run their handlers back to back, so the second one reads the first
// one's committed transitions instead of racing them.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
)

// Event endpoints for the idempotency table's ops visibility.
const (
	matchResultEndpoint   = "event:schedule.MatchResultRecorded"
	roundCompleteEndpoint = "event:schedule.RoundCompleted"
)

// Score is a settled match's final score in game's own vocabulary.
type Score struct {
	Home *int
	Away *int
}

// Odds is a settled match's payload odds.
type Odds struct {
	Home float64
	Away float64
}

// MatchResult is one settled match's facts, as game hears them.
type MatchResult struct {
	MatchID      int
	HomeTeamID   int
	AwayTeamID   int
	Score        Score
	Odds         Odds
	WinnerTeamID *int
}

// MatchResultRecorded is game's own copy of the calendar's match-result
// event: the ride-settlement command for one resolved match.
type MatchResultRecorded struct {
	EventID    string
	MatchID    int
	RoundID    int
	SeasonID   int
	HomeTeamID int
	AwayTeamID int
	Score      Score
	// Odds is the payload's claim about the committed rate. It is verified
	// against the committed match before anything is paid, never used on its
	// own; the side pick uses the committed team assignment.
	Odds Odds
	// WinnerTeamID crowns the result (nil on a draw).
	WinnerTeamID *int
}

// RoundCompleted is game's own copy of the calendar's round-completed event:
// the round-settlement command, carrying every settled match of the round.
type RoundCompleted struct {
	EventID  string
	RoundID  int
	SeasonID int
	// IsFinal marks the season's last round: undecided rides burn instead of
	// unlocking (ADR-022).
	IsFinal bool
	Results []MatchResult
}

// OnMatchResultRecorded settles the locked rides of one resolved match, from
// the payload: winner == ride.team → won_pending (the bonus accrues, the rule
// ride.WonPending encodes), otherwise lost (the accrued bonus is released).
func (s *Service) OnMatchResultRecorded(ctx context.Context, event MatchResultRecorded) error {
	return s.runEvent(ctx, matchResultEndpoint, event.EventID, event, event.RoundID, func(tx pgxTx) error {
		rides, err := s.rides.LockedForMatchTx(ctx, tx, event.MatchID)
		if err != nil {
			return err
		}

		for _, ride := range rides {
			if err := s.settleRideFromResult(ctx, tx, ride, event.WinnerTeamID, event.Odds); err != nil {
				return err
			}
		}
		return nil
	})
}

// OnRoundCompleted settles a closed round's rides, from the payload:
//
//  1. still-locked rides of the round's matches — settled per result, the
//     backstop for a MatchResultRecorded that never reached this process (the
//     accepted crash gap) or lost the resolve/settle race;
//  2. the undecided won_pending rides — force-resolved: unlocked on rounds
//     1-37 (the accrued bonus released), burned on the season's last round
//     (the chase option is unrepresentable once the season is over, the
//     tokens move to TB, the accrued bonus is kept — ADR-022).
//
// Statement order inside the transaction: locked settlements first, then the
// undecided sweep — a ride the backstop just settled to won_pending is swept
// by the same transaction, the old close-transaction semantics.
func (s *Service) OnRoundCompleted(ctx context.Context, event RoundCompleted) error {
	return s.runEvent(ctx, roundCompleteEndpoint, event.EventID, event, event.RoundID, func(tx pgxTx) error {
		for _, result := range event.Results {
			rides, err := s.rides.LockedForMatchTx(ctx, tx, result.MatchID)
			if err != nil {
				return err
			}
			for _, ride := range rides {
				if err := s.settleRideFromResult(ctx, tx, ride, result.WinnerTeamID, result.Odds); err != nil {
					return err
				}
			}
		}

		matchIDs := make([]int, 0, len(event.Results))
		for _, result := range event.Results {
			matchIDs = append(matchIDs, result.MatchID)
		}

		undecided, err := s.rides.UndecidedForMatchesTx(ctx, tx, matchIDs)
		if err != nil {
			return err
		}

		for _, ride := range undecided {
			if event.IsFinal {
				// The force-burn arrival keeps its accrued bonus (the 000013
				// coupling deliberately excludes burn): the tokens move to TB,
				// the bonus rides along.
				if _, err := s.rides.ArriveTx(ctx, tx, ride.ID, domain.RideWonPending, domain.RideBurned, ride.Version); err != nil {
					return err
				}
				if err := s.ledger.Burn(ctx, tx, ride.ID, ride.TokensLocked); err != nil {
					return err
				}
				continue
			}

			// The auto-unlock arrival carries no acc: release before the state
			// flip, the same order every unlocked arrival obeys.
			if err := s.ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
				return err
			}
			if _, err := s.rides.ArriveTx(ctx, tx, ride.ID, domain.RideWonPending, domain.RideUnlocked, ride.Version); err != nil {
				return err
			}
		}
		return nil
	})
}

// settleRideFromResult is the payload→destination derivation for one locked
// ride (score→state rule site 2 of 3 — the pure Go copy; resultAgreement is
// the enforcement home, and the calendar's Match.Winner is site 1, all tied
// by score_state_rule_test.go): won iff the result crowns the ride's team,
// lost otherwise (a draw crowns no winner). Then the agreement check, the
// transition and the money intents — the bonus paid at the COMMITTED odds,
// never the payload's.
func (s *Service) settleRideFromResult(ctx context.Context, tx pgxTx, ride domain.Ride, winner *int, claimedOdds Odds) error {
	destination := domain.RideLost
	if winner != nil && *winner == ride.TeamID {
		destination = domain.RideWonPending
	}

	// Result agreement (the app-level enforcement home of the old
	// rides_state_gate check 5 — migration 000017 moved it out of the DB with
	// the cross-context read it needed): the payload never decides alone. The
	// committed match is re-read (see the port method) and both of the
	// payload's claims are checked against it — the destination against ITS
	// scores, the odds against ITS odds — under the same refusals the trigger
	// raised plus the one for the rate: scores missing, destination
	// disagreeing, odds disagreeing. A lying payload fails here and the whole
	// settlement transaction rolls back — ErrInvalidTransition, the same
	// error the adapter maps the trigger's P0001 refusals to.
	committed, err := s.schedule.Match(ctx, ride.MatchID)
	if err != nil {
		return err
	}
	if err := resultAgreement(ride, destination, committed, claimedOdds); err != nil {
		return err
	}

	if destination == domain.RideLost {
		// The lost arrival carries no acc: release before the state flip (the
		// money-state coupling, same order as RideUnlock).
		if err := s.ledger.ReleaseBonus(ctx, tx, ride.ID); err != nil {
			return err
		}
	}

	if _, err := s.rides.ArriveTx(ctx, tx, ride.ID, domain.RideLocked, destination, ride.Version); err != nil {
		return err
	}

	if destination == domain.RideWonPending {
		// The win accrues the bonus at the ride's team odds — read off the
		// COMMITTED snapshot, teams and rate both: a payload that swapped
		// the sides could otherwise pass the odds-pair check and still hand
		// the ride the other team's rate.
		matchOdds, err := teamOdds(ride.TeamID, committed.HomeTeamID, committed.AwayTeamID, committed.Odds)
		if err != nil {
			return err
		}
		if err := s.ledger.AccrueBonus(ctx, tx, ride.ID, domain.BonusDelta(matchOdds, ride.TokensLocked, ride.Streak)); err != nil {
			return err
		}
	}

	return nil
}

// resultAgreement is the score→state rule's enforcement home (site 3 of 3;
// the calendar's Winner site 1, the payload derivation site 2, all tied by
// score_state_rule_test.go): the settlement payload's claims must agree with
// the COMMITTED match — the destination a locked ride arrives on follows the
// committed scores (won_pending requires the ride's team to have won, lost
// requires it not to have won, and scores must be there), and the rate the
// bonus is paid at must be the committed odds, not a rate the payload invented.
// The refusals keep the historical trigger names in their text
// (ride_result_scores_missing / ride_result_disagreement) plus their odds
// sibling (ride_result_odds_disagreement), and all three surface as
// ErrInvalidTransition (ADR-018: the match is the single source of truth, no
// writer may disagree with it).
func resultAgreement(ride domain.Ride, destination domain.RideState, committed port.MatchSnapshot, claimedOdds Odds) error {
	if committed.Score.Home == nil || committed.Score.Away == nil {
		return fmt.Errorf("ride %d arrival refused: match %d carries no scores (ride_result_scores_missing): %w",
			ride.ID, committed.ID, domain.ErrInvalidTransition)
	}

	teamWon := (ride.TeamID == committed.HomeTeamID && *committed.Score.Home > *committed.Score.Away) ||
		(ride.TeamID == committed.AwayTeamID && *committed.Score.Away > *committed.Score.Home)

	if (destination == domain.RideWonPending) != teamWon {
		return fmt.Errorf("ride %d arrival refused: destination %s disagrees with the committed result of match %d (ride_result_disagreement): %w",
			ride.ID, destination, committed.ID, domain.ErrInvalidTransition)
	}

	// The rate is a claim like the destination: the payload's odds pair must
	// BE the committed pair. A rate game cannot match is a loud error, not a
	// rate to use (a stale or lying publisher is a bug, never a fact).
	if claimedOdds.Home != committed.Odds.Home || claimedOdds.Away != committed.Odds.Away {
		return fmt.Errorf("ride %d arrival refused: the payload odds %.2f/%.2f disagree with the committed odds %.2f/%.2f of match %d (ride_result_odds_disagreement): %w",
			ride.ID, claimedOdds.Home, claimedOdds.Away, committed.Odds.Home, committed.Odds.Away, committed.ID, domain.ErrInvalidTransition)
	}

	return nil
}

// teamOdds picks the ride's team side of the committed odds. A ride whose
// team is on neither side is corrupt data — refuse rather than guess.
func teamOdds(teamID, homeTeamID, awayTeamID int, odds port.Odds) (decimal.Decimal, error) {
	switch teamID {
	case homeTeamID:
		return decimal.NewFromFloat(odds.Home), nil
	case awayTeamID:
		return decimal.NewFromFloat(odds.Away), nil
	default:
		return decimal.Decimal{}, fmt.Errorf("ride team %d is on neither side of the match (%d vs %d)", teamID, homeTeamID, awayTeamID)
	}
}

// runEvent is the handlers' shared execution shape: claim the EventID first
// (inside the handler's transaction — a failed handler rolls its claim back,
// so an unprocessed event can be re-delivered; a replay is a no-op success),
// then the per-round settlement lock (see the file comment: the two
// settlement handlers of one round run back to back, never beside each other
// — a replay skips the lock with its no-op), then the settlement work, then
// the stored response, one commit.
func (s *Service) runEvent(ctx context.Context, endpoint, eventID string, payload any, roundID int, exec func(pgxTx) error) error {
	tx, err := s.transactor.Begin(ctx)
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

	token := port.Token{Key: eventID, Endpoint: endpoint, Hash: hash[:]}
	claimed, _, err := s.idempotency.Claim(ctx, tx, token)
	if err != nil {
		return err
	}
	if !claimed {
		// Replay: the event already ran, byte-identical delivery is a no-op
		// success.
		return nil
	}

	// The per-round settlement lock, before any ride is read: the other
	// settlement handler of this round (a resolve's and a close's publishes
	// overlap when they fire at the same moment) commits its transitions
	// first, so this transaction's snapshot carries them.
	if err := s.transactor.LockSettlementRound(ctx, tx, roundID); err != nil {
		return err
	}

	if err := exec(tx); err != nil {
		return err
	}

	summary, err := json.Marshal(struct{ EventID string }{EventID: eventID})
	if err != nil {
		return err
	}
	if err := s.idempotency.Save(ctx, tx, token, port.SavedResponse{Status: 200, Body: summary}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
