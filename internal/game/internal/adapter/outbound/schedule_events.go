package outbound

// The event seam's async anti-corruption layer. The calendar publishes
// schedule.MatchResultRecorded and schedule.RoundCompleted post-commit; those
// payloads are the CALENDAR's public contract and stay so. This file
// translates them into game's own command types (service.MatchResultRecorded,
// service.RoundCompleted) at the boundary, so the core never imports — or
// even knows the name of — a schedule event.
//
// The translation is deliberately field-for-field: same facts, same
// identifiers, same ordering. Only the vocabulary changes (the calendar's
// Score/Odds/MatchResult shapes become game's), which is exactly what the
// anti-corruption layer is for.

import (
	"context"

	"lt-api.aleksrdvn.com/internal/game/internal/core/service"
	"lt-api.aleksrdvn.com/internal/kernel/eventbus"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// EventHandler is the game-side receiver of the translated events: the
// settlement use cases. Declared consumer-side so this adapter depends on the
// use-case shape, not on the concrete service.
type EventHandler interface {
	OnMatchResultRecorded(ctx context.Context, event service.MatchResultRecorded) error
	OnRoundCompleted(ctx context.Context, event service.RoundCompleted) error
}

// Subscribe wires the game settlement handlers onto the process event bus,
// translating each calendar payload as it arrives. Delivery timing is
// unchanged: the calendar publishes post-commit, handlers run in-process and
// synchronously, and a handler error propagates back to the publisher exactly
// as it did before the translation layer existed.
// rule:R-GAME-026
func Subscribe(bus *eventbus.Bus, handler EventHandler) {
	eventbus.Subscribe(bus, func(ctx context.Context, event schedule.MatchResultRecorded) error {
		return handler.OnMatchResultRecorded(ctx, TranslateMatchResultRecorded(event))
	})
	eventbus.Subscribe(bus, func(ctx context.Context, event schedule.RoundCompleted) error {
		return handler.OnRoundCompleted(ctx, TranslateRoundCompleted(event))
	})
}

// TranslateMatchResultRecorded translates one calendar match-result payload
// into game's settlement command. Exported alongside Subscribe so a caller
// that drives the seam directly (the composition root's tests) carries the
// payload across the boundary instead of duplicating the mapping.
func TranslateMatchResultRecorded(event schedule.MatchResultRecorded) service.MatchResultRecorded {
	return service.MatchResultRecorded{
		EventID:      event.EventID,
		MatchID:      event.MatchID,
		RoundID:      event.RoundID,
		SeasonID:     event.SeasonID,
		HomeTeamID:   event.HomeTeamID,
		AwayTeamID:   event.AwayTeamID,
		Score:        scoreFrom(event.Score),
		Odds:         oddsFrom(event.Odds),
		WinnerTeamID: event.WinnerTeamID,
	}
}

// TranslateRoundCompleted translates one calendar round-completed payload,
// including every settled match it carries.
func TranslateRoundCompleted(event schedule.RoundCompleted) service.RoundCompleted {
	results := make([]service.MatchResult, 0, len(event.Results))
	for _, result := range event.Results {
		results = append(results, TranslateMatchResult(result))
	}

	return service.RoundCompleted{
		EventID:  event.EventID,
		RoundID:  event.RoundID,
		SeasonID: event.SeasonID,
		IsFinal:  event.IsFinal,
		Results:  results,
	}
}

// TranslateMatchResult translates one calendar match result (the nested
// payload of RoundCompleted).
func TranslateMatchResult(result schedule.MatchResult) service.MatchResult {
	return service.MatchResult{
		MatchID:      result.MatchID,
		HomeTeamID:   result.HomeTeamID,
		AwayTeamID:   result.AwayTeamID,
		Score:        scoreFrom(result.Score),
		Odds:         oddsFrom(result.Odds),
		WinnerTeamID: result.WinnerTeamID,
	}
}

// scoreFrom translates a score: both sides are pointers (nil before the
// result exists), so the boundary hands the same pointers on.
func scoreFrom(score schedule.Score) service.Score {
	return service.Score{Home: score.Home, Away: score.Away}
}

// oddsFrom translates the odds pair.
func oddsFrom(odds schedule.Odds) service.Odds {
	return service.Odds{Home: odds.Home, Away: odds.Away}
}
