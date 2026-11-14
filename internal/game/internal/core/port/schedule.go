package port

// The calendar port: the narrow slice of the schedule context the ride
// domain consumes (ADR-028). Its vocabulary is game's own — Phase,
// MatchSnapshot, NextMatch, Team, Season — and its errors are game's
// sentinels, so the core never learns the calendar's types. The
// anti-corruption layer at adapter/outbound (scheduleacl.go) is the one game
// package that translates schedule's facade into this port.
//
// What game may NOT do against the calendar: raw SQL against
// matches/rounds (the guarded statements live on the calendar's side), and
// deriving ride outcomes from calendar rows (the settlement payloads carry
// them; MatchSnapshot exists only to re-verify a payload against the
// committed result).

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
)

// NextMatch is the continuation destination RideLock picks: the team's next
// live match. Game reads only the identity of the destination.
type NextMatch struct {
	ID         int
	StartsAt   time.Time
	HomeTeamID int
	AwayTeamID int
}

// Score is a committed match's final score (nil before the result exists).
type Score struct {
	Home *int
	Away *int
}

// MatchSnapshot is the committed facts of one match — the input of the
// result-agreement check the settlement handler runs (the app-level
// enforcement of the old rides_state_gate check 5, migration 000017).
type MatchSnapshot struct {
	ID         int
	HomeTeamID int
	AwayTeamID int
	Score      Score
}

// Team is one team's identity — the read behind ride create's team reference
// and the player use cases' favourite-team check.
type Team struct {
	ID   int
	Name string
}

// Season is one season's identity — the read behind player create's season
// reference.
type Season struct {
	ID int
}

// Schedule is the calendar facts the game use cases consume. Every refusal
// is a game sentinel: domain.ErrNotFound for an unknown record,
// domain.ErrRoundNotOpen for a round that refuses commands,
// domain.ErrNoNextMatch for a team with no destination left.
type Schedule interface {
	// PhaseForMatch reports the phase of the round the match belongs to, at
	// the given instant (the caller's clock — the domain never reads time
	// itself). domain.ErrRoundNotOpen when the round is not open,
	// domain.ErrNotFound when the match is unknown.
	PhaseForMatch(ctx context.Context, matchID int, now time.Time) (domain.Phase, error)

	// NextForTeam returns the team's next live match — the continuation
	// pick behind RideLock. domain.ErrNoNextMatch when there is none.
	NextForTeam(ctx context.Context, teamID int) (NextMatch, error)

	// RoundOpenTx pins the ride's own round OPEN at the write instant inside
	// the caller's transaction (the calendar's FOR SHARE gate).
	// domain.ErrNotFound when the match is unknown, domain.ErrRoundNotOpen
	// when the round refuses commands.
	RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error

	// DestinationOpenTx pins a continuation destination LIVE at the write
	// instant inside the caller's transaction. Every refusal (unknown,
	// ended, round closed) is domain.ErrNotFound — the caller maps it to the
	// round refusal.
	DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error

	// Match returns the committed facts of one match — the input of the
	// result-agreement check. domain.ErrNotFound when the match is unknown.
	Match(ctx context.Context, matchID int) (MatchSnapshot, error)

	// Team reports one team's existence. domain.ErrNotFound when unknown.
	Team(ctx context.Context, teamID int) (Team, error)

	// Season reports one season's existence. domain.ErrNotFound when
	// unknown.
	Season(ctx context.Context, seasonID int) (Season, error)
}
