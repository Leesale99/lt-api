package game

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/schedule"
)

// SchedulePort is the narrow slice of the schedule context the ride domain
// consumes (ADR-028): the calendar facts its commands need, nothing more.
// *schedule.Store satisfies it structurally, so the composition root wires
// the concrete store and no adapter exists — the interface is the boundary
// line, not a layer.
//
// What game may NOT do against schedule anymore: raw SQL against
// matches/rounds (the guarded statements' EXISTS predicates and the
// resolve's score write moved — the resolve is schedule's
// Service.ResolveMatch script, the round guards are the two Tx methods
// below, which run schedule's own SQL inside the ride command's
// transaction), and deriving ride outcomes from schedule rows (the
// MatchResultRecorded/RoundCompleted payloads carry them).
type SchedulePort interface {
	// PhaseForMatch: the ride's round status plus the phase window
	// (first match start, last match end) — the inputs of the phase gate
	// behind every ride command.
	PhaseForMatch(ctx context.Context, matchID int) (schedule.RoundStatus, *time.Time, *time.Time, error)

	// NextForTeam: the continuation pick behind RideLock.
	NextForTeam(ctx context.Context, teamID int) (schedule.Match, error)

	// RoundOpenTx pins the ride's own round OPEN at the write instant
	// inside the caller's transaction (FOR SHARE on the round row).
	// store.ErrRecordNotFound when the match is unknown.
	RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) (schedule.RoundStatus, error)

	// DestinationOpenTx pins a continuation destination LIVE at the write
	// instant inside the caller's transaction. Every refusal (unknown,
	// ended, round closed) is store.ErrRecordNotFound — the caller maps it
	// to the round-refusal.
	DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error

	// Match returns the committed facts of one match — the input of the
	// result-agreement check the settlement handlers run (the app-level
	// enforcement of the old rides_state_gate check 5, migration 000017).
	// The event payload is never trusted alone: the committed scores are
	// the fact the ride destination must agree with (ADR-018). ErrRecord-
	// NotFound when the match is unknown.
	Match(ctx context.Context, matchID int) (schedule.Match, error)

	// Team reports one team's existence — the app-level check behind ride
	// create's team reference (000016: rides_team_id_fkey is gone with
	// the cross-context FKs, migration 000016; the api-level team-existence
	// surface is schedule's, the ride command reads it through the port).
	// ErrRecordNotFound when the team is unknown.
	Team(ctx context.Context, teamID int) (schedule.Team, error)
}
