package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/store"
)

// Port methods on *Store: the narrow surface the game context consumes
// (game.SchedulePort, satisfied structurally — ADR-028). The SQL here is
// schedule's own (matches/rounds), executed on the CALLER's transaction
// where noted: the write-instant round guards of the ride commands are
// schedule facts, so the lock-taking statements live on this side of the
// seam while the ride writes stay on game's side.
//
// The resolve half of the old store-level coupling (ResolveTx's
// guarded score write) is schedule's own script now (Service.ResolveMatch);
// game no longer needs it through the port.
//
// The context privilege boundary (migration 000018) denies
// game_app every schedule table, so the in-caller's-tx guards cannot be
// plain SELECTs — row locking (FOR SHARE) needs UPDATE privilege, and
// granting that would let game corrupt schedule state. The two guards
// are schedule's own SECURITY DEFINER functions instead
// (schedule.round_open_guard / schedule.destination_open_guard, created
// by 000018): schedule's SQL runs as the schema owner, takes the same
// row locks inside the caller's transaction, and returns only the fact.
// The pool-read port methods (PhaseForMatch, NextForTeam, Match) run on
// the schedule pool, under schedule_app, and schema-qualify their tables
// so the statements are honest about crossing the boundary wherever they
// happen to execute.

// PhaseForMatch returns the ride-phase inputs for the round the given
// match belongs to, in one statement: the round status (the ErrRoundNotOpen
// gate input) plus the phase window — min(starts_at), max(ended_at) over
// the round's matches.
//
// One statement, not three reads: the round-status gate and the window
// come from the same snapshot with no explicit transaction. Phase stays
// derived in Go from these facts (ADR-017); the store returns only facts.
//
// GROUP BY r.id suffices for selecting r.status: r.id is rounds' primary
// key, so PostgreSQL treats every other rounds column as functionally
// dependent (single-valued per group).
func (s *Store) PhaseForMatch(ctx context.Context, matchID int) (RoundStatus, *time.Time, *time.Time, error) {
	return s.Matches.PhaseForMatch(ctx, matchID)
}

// NextForTeam returns the team's next live match — the continuation pick
// behind RideLock.
func (s *Store) NextForTeam(ctx context.Context, teamID int) (Match, error) {
	return s.Matches.NextForTeam(ctx, teamID)
}

// RoundOpenTx pins the ride's own round OPEN at the write instant, inside
// the caller's transaction: the SELECT takes FOR SHARE on the round row,
// so a concurrent round close (whose guarded flip takes the row's write
// lock) serializes with it — the command either sees the still-open round
// or, after the close commits, the closed one. Under READ COMMITTED a
// blocked FOR SHARE re-reads the newest committed row before evaluating,
// which is exactly the EvalPlanQual re-check the pre-Phase-C guarded
// statement got from its EXISTS predicate.
//
// ErrRecordNotFound when the match is unknown; the status otherwise — the
// caller (game) maps a non-open round to its round-refusal sentinel.
func (s *Store) RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) (RoundStatus, error) {
	// The guard is schedule's definer function (see the file header): same
	// statement shape as before, executed as the schema owner. NULL means
	// the match is unknown — the not-found mapping is unchanged.
	var status *RoundStatus

	err := tx.QueryRow(ctx, `SELECT schedule.round_open_guard($1)`, matchID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", store.ErrRecordNotFound
	case err != nil:
		return "", err
	case status == nil:
		return "", store.ErrRecordNotFound
	default:
		return *status, nil
	}
}

// DestinationOpenTx pins a continuation destination LIVE at the write
// instant, inside the caller's transaction: un-ended match, round not
// closed. Both rows are share-locked, so a concurrent resolve of the
// destination or close of its round serializes before the caller's ride
// write — the "destination stopped between pick and write" race loses here
// instead of inside the ride statement.
//
// store.ErrRecordNotFound covers every refusal cause (unknown match,
// already ended, round closed): the caller maps them all to the
// round-refusal, the Phase 04 contract for a stale pick.
func (s *Store) DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	// The guard is schedule's definer function (see the file header): FALSE
	// covers every refusal cause (unknown, ended, round closed), the same
	// mapping the previous statement's 0 rows had.
	var live bool

	err := tx.QueryRow(ctx, `SELECT schedule.destination_open_guard($1)`, matchID).Scan(&live)
	switch {
	case err != nil:
		return err
	case !live:
		return store.ErrRecordNotFound
	default:
		return nil
	}
}

// Match returns the committed facts of one match — the read behind game's
// result-agreement check (the app-level translation of the old
// rides_state_gate check 5, migration 000017): the settlement handlers
// verify the event payload against the committed scores before arriving a
// ride on won_pending|lost. The scores are committed by the time game
// sees them (publish is post-commit, and ResolveMatch refuses a
// re-resolution), so a plain pool read — schedule pool, schedule_app — is
// the right snapshot; no in-tx read is needed.
func (s *Store) Match(ctx context.Context, matchID int) (Match, error) {
	return s.Matches.Get(ctx, matchID)
}

// Team returns one team — the read behind game's ride-create team
// existence check (see the port interface comment).
func (s *Store) Team(ctx context.Context, teamID int) (Team, error) {
	return s.Teams.Get(ctx, teamID)
}
