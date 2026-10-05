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
	"time"

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

// insertSettledFutureMatch plants the create-vs-close seam fixture: a match
// already closed (settled, scored) whose starts_at is still in the future.
// The close's gate sees every match settled; the create's phase derivation
// sees a round whose matches have not started (ActionPhase). Real data
// cannot hold this shape — a match settles only after it starts — but the
// race these two commands run on the round row does not care: each side's
// own gate is satisfied, so the outcome is decided by the write instant
// alone.
func insertSettledFutureMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, lead string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 88, 79, 'closed', now() + $5::interval, now() + $5::interval + interval '2 hours')
		RETURNING id
	`, seasonID, roundID, homeID, awayID, lead).Scan(&id)
	if err != nil {
		t.Fatalf("insert settled future match: %v", err)
	}

	return id
}

// waitUntilInsertBlocked polls pg_stat_activity until the given goroutine's
// ride insert is actually waiting on the round row's lock. The scripted
// close-first race relies on this ordering: without the wait, a slow
// goroutine could see the committed close and be refused for the mundane
// reason, proving nothing about the lock.
func waitUntilInsertBlocked(ctx context.Context, t *testing.T) {
	t.Helper()

	for i := 0; i < 100; i++ {
		var waiting int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%INSERT INTO rides%'
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ride insert never blocked on the round row lock")
}

func TestService_RideCreateRacesRoundClose(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc := NewService(NewStore(pool))

	t.Run("close wins the write instant: the guarded insert is refused", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertSettledFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		// The close command's status flip, held uncommitted: updateRound
		// takes the round row's lock exactly like Rounds.Close does. Both
		// gates pass — the match is settled, no ride exists yet.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin close tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		var round Round
		if err := pool.QueryRow(ctx, `
			SELECT id, season_id, number, status, version
			FROM rounds WHERE id = $1
		`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
			t.Fatalf("read round: %v", err)
		}
		round.Status = RoundClosed
		if _, err := NewStore(pool).Rounds.updateRound(ctx, tx, round); err != nil {
			t.Fatalf("uncommitted close flip: %v", err)
		}

		// RidePhase's read sees the round open (the close is uncommitted),
		// but the guarded insert blocks on its FOR SHARE until the close
		// commits, then re-evaluates the WHERE against the committed row.
		var (
			done      = make(chan struct{})
			createErr error
		)
		go func() {
			defer close(done)
			_, createErr = svc.RideCreate(ctx, Ride{
				PlayerID:     playerID,
				TeamID:       homeID,
				MatchID:      matchID,
				TokensLocked: decimal.NewFromInt(100),
				BaseAtLock:   decimal.NewFromInt(95),
			})
		}()
		waitUntilInsertBlocked(ctx, t)

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit close: %v", err)
		}
		<-done

		if !errors.Is(createErr, ErrRoundNotOpen) {
			t.Fatalf("RideCreate() = %v, want ErrRoundNotOpen", createErr)
		}

		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM rides WHERE match_id = $1`, matchID).Scan(&n); err != nil {
			t.Fatalf("count rides: %v", err)
		}
		if n != 0 {
			t.Fatalf("rides on the closed round's match = %d, want 0 (no live ride inside a closed round)", n)
		}
	})

	t.Run("create wins the write instant: the ride survives the close", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertSettledFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		ride, err := svc.RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})
		if err != nil {
			t.Fatalf("RideCreate() = %v, want nil", err)
		}
		if ride.State != RideLocked {
			t.Fatalf("state = %q, want locked", ride.State)
		}

		// The close commits after the create: the ride is locked, outside
		// the close's auto-resolution set (won_pending only), and the close
		// gates see no undecided rides — the round closes around it.
		var round Round
		if err := pool.QueryRow(ctx, `
			SELECT id, season_id, number, status, version
			FROM rounds WHERE id = $1
		`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
			t.Fatalf("read round: %v", err)
		}
		round.Status = RoundClosed
		if _, err := NewStore(pool).Rounds.Close(ctx, round); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}

		got, err := NewStore(pool).Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if got.State != RideLocked {
			t.Errorf("state = %q, want locked (the create committed first — legal order)", got.State)
		}
	})
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
