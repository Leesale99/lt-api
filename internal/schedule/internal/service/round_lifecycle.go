package service

import (
	"context"
	"strconv"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// UpdateRound is the round PATCH use case: the lifecycle transition dispatch
// the HTTP handler used to own. The handler decides request shape (IDs, the
// X-Expected-Version header, the decoded patch) and merges it onto the stored
// round; the transition decision is domain knowledge and lives here:
//
//   - created → open goes through Open, whose season flip must commit with
//     the round flip (ADR-008 point 3);
//   - open → closed goes through Close, which publishes RoundCompleted
//     post-commit (ADR-022 / ADR-027) — ride settlement is eventual, behind
//     the event;
//   - every other transition is a plain Update.
//
// expectedVersion is the caller's X-Expected-Version header value ("" when
// absent). The handler already refused a mismatching value before parsing
// the body — that ordering is the wire contract and stays there; this call
// re-verifies it so the use case never trusts that its caller checked.
func (s *RoundService) UpdateRound(ctx context.Context, current, updated model.Round, expectedVersion string) (model.Round, error) {
	if expectedVersion != "" && strconv.Itoa(current.Version) != expectedVersion {
		return model.Round{}, persistence.ErrEditConflict
	}

	// ADR-008 point 3: the created → open transition also starts the season
	// (open → in_progress), and the two writes must succeed together — so it
	// goes through Open's transaction rather than Update.
	switch {
	case current.Status == model.RoundCreated && updated.Status == model.RoundOpen:
		return s.Open(ctx, updated)
	case current.Status == model.RoundOpen && updated.Status == model.RoundClosed:
		// ADR-022 (event seam): closing flips the round (and the season, on
		// the last round) and publishes RoundCompleted post-commit — the game
		// context settles the undecided rides from the event payload
		// (auto-unlock rounds 1-37, auto-burn round 38). Ride settlement is
		// no longer this transaction's business, so the PATCH stays one tx
		// like the open transition.
		return s.Close(ctx, updated)
	default:
		return s.repo.Update(ctx, updated)
	}
}

// Close transitions a round open → closed and publishes RoundCompleted
// post-commit (ADR-027): the ride settlement is game's, driven by this
// event. The repository commits the flip (and the last round's season
// close); the publish — the results read and the delivery — happens here,
// after that commit, exactly where it used to run inside the store.
func (s *RoundService) Close(ctx context.Context, round model.Round) (model.Round, error) {
	closed, err := s.repo.Close(ctx, round)
	if err != nil {
		return model.Round{}, err
	}

	// Post-commit publish (ADR-027). Fat payload: the round's full result
	// set rides in the event, so game's settlement never joins back into
	// schedule's tables.
	if s.bus != nil {
		results, err := s.matches.ResultsForRound(ctx, closed.ID)
		if err != nil {
			s.logCloseGap(ctx, closed, "results read", err)
			return closed, nil
		}
		event := model.RoundCompleted{
			EventID:  model.NewEventID(),
			RoundID:  closed.ID,
			SeasonID: closed.SeasonID,
			IsFinal:  closed.Number == model.RoundsPerSeason,
			Results:  results,
		}
		if err := s.bus.Publish(context.WithoutCancel(ctx), event); err != nil && s.log != nil {
			// Crash gap: the close is committed; a failed settlement is the
			// gap the Phase 07 sweep will close. The request must not
			// fail — its write is already durable.
			s.log.Warn("event handler failed after commit; awaiting the Phase 07 sweep",
				"event_type", "schedule.RoundCompleted", "round_id", closed.ID, "error", err)
		}
	}

	return closed, nil
}

// logCloseGap reports a dropped post-commit event at the close site.
func (s *RoundService) logCloseGap(ctx context.Context, round model.Round, stage string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn("RoundCompleted event dropped after commit; awaiting the Phase 07 sweep",
		"stage", stage, "round_id", round.ID, "error", err)
}
