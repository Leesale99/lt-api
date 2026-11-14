package port

// The repositories the ride use cases drive: consumer-side interfaces (the
// game core declares what it needs; the Postgres adapter at
// adapter/outbound/postgres implements it). Every method speaks the domain's
// values and sentinels — no pgx row, no SQLSTATE, no HTTP shape crosses
// here.
//
// The *Tx methods run on the caller's transaction: every ride command and
// settlement handler is one unit of work, so the claim, the guards' locks and
// the write share it (ADR-024).

import (
	"context"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// RideRepository stores rides — and only rides. Calendar facts reach the
// domain through the Schedule port; the money columns are the ledger's.
type RideRepository interface {
	// Get reads one ride. domain.ErrNotFound when it does not exist.
	Get(ctx context.Context, id int) (domain.Ride, error)

	// GetAll reads the ride list under the given filters (zero values mean
	// "no filter").
	GetAll(ctx context.Context, id, playerID, teamID, matchID int, state domain.RideState, filters persistence.Filters) ([]domain.Ride, persistence.Metadata, error)

	// Insert admits locked only: rides are created locked (ADR-019).
	Insert(ctx context.Context, ride domain.Ride) (domain.Ride, error)

	// InsertTx writes the created ride on the caller's transaction.
	InsertTx(ctx context.Context, tx pgx.Tx, ride domain.Ride) (domain.Ride, error)

	// LockedForMatchTx reads the locked rides of one match on the caller's
	// transaction — the match-result settlement's scan.
	LockedForMatchTx(ctx context.Context, tx pgx.Tx, matchID int) ([]domain.Ride, error)

	// UndecidedForMatchesTx reads the undecided (won_pending) rides of the
	// given matches on the caller's transaction — the round settlement's
	// sweep.
	UndecidedForMatchesTx(ctx context.Context, tx pgx.Tx, matchIDs []int) ([]domain.Ride, error)

	// ArriveTx writes one settlement arrival (from-state → to-state) on the
	// caller's transaction. A gone ride is domain.ErrNotFound, a moved row
	// domain.ErrInvalidTransition.
	ArriveTx(ctx context.Context, tx pgx.Tx, rideID int, from, to domain.RideState, claimedVersion int) (domain.Ride, error)

	// Update writes state, continuation and streak under the optimistic
	// version guard.
	Update(ctx context.Context, ride domain.Ride) (domain.Ride, error)

	// DecisionGuardedTx writes a ride decision (Lock, Burn, Unlock) under the
	// won_pending + version guard, on the caller's transaction.
	DecisionGuardedTx(ctx context.Context, tx pgx.Tx, ride domain.Ride) (domain.Ride, error)

	// ExistsForMatch reports whether any ride sits on the match — the game
	// half of the calendar's match-delete refusal (000016: rides_match_id_fkey's
	// RESTRICT is gone with the cross-context FKs).
	ExistsForMatch(ctx context.Context, matchID int) (bool, error)

	// ExistsForTeam reports whether any ride names the team — the game half
	// of the calendar's team-delete refusal.
	ExistsForTeam(ctx context.Context, teamID int) (bool, error)
}
