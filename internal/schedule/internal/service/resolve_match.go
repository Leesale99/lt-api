package service

import (
	"context"
	"time"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

// ErrEndedAtInFuture is re-exported from the model package: the service door
// raises it, the model owns it (it is time policy, part of the context's
// vocabulary).
var ErrEndedAtInFuture = model.ErrEndedAtInFuture

// ResolveMatch closes a match: the guarded score write (MatchRepository.ResolveTx)
// in one transaction, then — after that commit — the MatchResultRecorded
// event that hands the result to the game context for ride settlement
// (ADR-027). Time policy is checked at the door (ADR-026), before any
// transaction: never open a tx just to fail cheaply, and never let a
// legality answer be outranked by a storage conflict (e.g. a closed match
// would turn this into a 409 if the check ran after ResolveTx).
//
// Publish is post-commit and synchronous: by the time ResolveMatch returns,
// the game handler has settled the locked rides of the match (or the
// settlement error has been logged as the accepted gap — the commit
// above is already durable, so a failed handler must not fail the request).
func (s *Service) ResolveMatch(ctx context.Context, matchID int, score model.Score, endedAt time.Time, version int) (model.Match, error) {
	if !endedAt.Before(s.now()) {
		return model.Match{}, ErrEndedAtInFuture
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Match{}, err
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	match, err := s.matches.ResolveTx(ctx, tx, matchID, score, endedAt, version)
	if err != nil {
		return model.Match{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Match{}, err
	}

	// Post-commit publish (ADR-027). The winner derivation is Match.Winner —
	// site 1 of the score→state rule; game's settlement derives ride
	// destinations from this payload and the rides_state_gate re-checks the
	// same facts against the committed match row, so publisher and
	// subscriber cannot disagree silently.
	event := model.MatchResultRecorded{
		EventID:      model.NewEventID(),
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
	publish(ctx, s.bus, s.log, event)

	return match, nil
}
