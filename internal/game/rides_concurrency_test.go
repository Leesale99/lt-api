package game

// Concurrency tests for the ride commands' guarded write (Phase 04 TOCTOU
// sweep): real goroutines racing on one ride row under READ COMMITTED,
// asserting that only the two legal interleavings exist and that they land
// on exactly the states the design map allows:
//
//	close wins:  the ride is auto-unlocked, the continuation is refused
//	             with ErrRoundNotOpen (the round-refusal 409);
//	lock wins:   the ride is locked onto the destination, and the close's
//	             auto-unlock re-checks its own WHERE against the committed
//	             row and drops it — the ride continues into the next round.
//
// Nothing here pins an interleaving order: both orders are legal, and the
// assertions hold under either. What must never appear is a third shape —
// a decision applied to a ride whose round already closed.
//
// The two-burn race (the concurrent-burn task's mechanism, arrived at via
// the same guarded write) proves the loser never lands its write: for two
// identical commands the guard's state/version predicates refuse the
// second write before rides_state_gate would.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
)

// raceIterations is how many fresh fixtures each race test re-runs. Each
// iteration replants the whole hierarchy (cleanup cascades), so this stays
// small; the value only widens the odds of exercising both interleavings.
const raceIterations = 5

func TestService_RideLockRacesRoundClose(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc := NewService(NewStore(pool))

	for i := 0; i < raceIterations; i++ {
		t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
			roundID, homeID, awayID := seedFixture(ctx, t)
			closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
			ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

			if _, err := pool.Exec(ctx, `INSERT INTO rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
				t.Fatalf("insert round 2: %v", err)
			}
			destination := insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

			// The close command needs the round row as its callers supply
			// it: read the stored facts, and make the transition decision
			// the handler makes after ValidateRoundUpdate — the update
			// carries the target status, not the stored one.
			var round Round
			if err := pool.QueryRow(ctx, `
				SELECT id, season_id, number, status, version
				FROM rounds WHERE id = $1
			`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
				t.Fatalf("read round: %v", err)
			}
			if round.Status != RoundOpen {
				t.Fatalf("fixture round status = %q, want open", round.Status)
			}
			round.Status = RoundClosed

			var (
				wg       sync.WaitGroup
				lockErr  error
				closeErr error
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				// One key per iteration: the game fixture's cleanup does not
				// truncate idempotency_keys, so a reused key would replay the
				// previous iteration's stored response instead of racing.
				token := rideToken(fmt.Sprintf("race-lock-%d", i), "POST /v1/rides/:id/lock")
				_, lockErr = svc.RideLock(ctx, ride.ID, token, idempotentMarshal)
			}()
			go func() {
				defer wg.Done()
				_, closeErr = NewStore(pool).Rounds.Close(ctx, round)
			}()
			wg.Wait()

			// Nothing in either fixture or schedule refuses the close: its
			// only match is closed, and any ride it still sees as
			// won_pending is auto-unlocked by its own first statement.
			if closeErr != nil {
				t.Fatalf("Close() = %v, want nil", closeErr)
			}

			got, err := NewStore(pool).Rides.Get(ctx, ride.ID)
			if err != nil {
				t.Fatalf("Get() = %v, want nil", err)
			}

			switch {
			case lockErr == nil:
				// The continuation committed before the close: the ride is
				// locked on the destination, already outside the closing
				// round's auto-unlock set.
				if got.State != RideLocked || got.MatchID != destination {
					t.Errorf("lock won but state/match = %q/%d, want locked/%d", got.State, got.MatchID, destination)
				}
			case errors.Is(lockErr, ErrRoundNotOpen):
				// The close committed first: the ride was auto-unlocked and
				// the continuation's guarded write refused it.
				if got.State != RideUnlocked {
					t.Errorf("close won but state = %q, want unlocked", got.State)
				}
			default:
				t.Fatalf("RideLock() = %v, want nil or ErrRoundNotOpen", lockErr)
			}

			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, roundID).Scan(&status); err != nil {
				t.Fatalf("read round status: %v", err)
			}
			if status != "closed" {
				t.Errorf("round status = %q, want closed in both outcomes", status)
			}
		})
	}
}

func TestService_RideBurnRacesExactlyOnce(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc := NewService(NewStore(pool))

	for i := 0; i < raceIterations; i++ {
		t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
			roundID, homeID, awayID := seedFixture(ctx, t)
			closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
			ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

			// Two independent requests (distinct keys), not a client retry:
			// both claim their key and race the guarded write — the loser must
			// never land its write. A same-key retry race is single-flight by
			// claim instead (the idempotency tests pin that shape).
			keys := []string{
				fmt.Sprintf("race-burn-a-%d", i),
				fmt.Sprintf("race-burn-b-%d", i),
			}

			var (
				wg   sync.WaitGroup
				errs = make([]error, 2)
			)
			wg.Add(2)
			for j := range errs {
				go func(k int) {
					defer wg.Done()
					_, errs[k] = svc.RideBurn(ctx, ride.ID, rideToken(keys[k], "POST /v1/rides/:id/burn"), idempotentMarshal)
				}(j)
			}
			wg.Wait()

			winners := 0
			for _, err := range errs {
				switch {
				case err == nil:
					winners++
				case errors.Is(err, ErrRoundNotOpen), errors.Is(err, ErrInvalidTransition):
					// The loser either lost the guarded write (round sentinel)
					// or its advisory read landed after the winner's commit
					// (the transition graph refuses a burned ride).
				default:
					t.Fatalf("RideBurn() = %v, want nil, ErrRoundNotOpen or ErrInvalidTransition", err)
				}
			}
			if winners != 1 {
				t.Fatalf("winners = %d (errs: %v), want exactly 1", winners, errs)
			}

			got, err := NewStore(pool).Rides.Get(ctx, ride.ID)
			if err != nil {
				t.Fatalf("Get() = %v, want nil", err)
			}
			if got.State != RideBurned {
				t.Errorf("state = %q, want burned", got.State)
			}
			if got.Version != ride.Version+1 {
				t.Errorf("version = %d, want %d (exactly one write landed)", got.Version, ride.Version+1)
			}
		})
	}
}
