package game

// Concurrency tests for the ride commands' guarded write (Phase 04 TOCTOU
// sweep): real goroutines racing on one ride row under READ COMMITTED,
// asserting that only the two legal interleavings exist and that they land
// on exactly the states the design map allows:
//
//	close wins:  the round closes, the continuation is refused with
//	             ErrRoundNotOpen (the round-refusal 409); the undecided
//	             ride settles later, behind RoundCompleted.
//	lock wins:   the ride is locked onto the destination, and the close
//	             finds no undecided ride of its own to settle — the ride
//	             continues into the next round.
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
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/store"

	"lt-api.aleksrdvn.com/internal/schedule"
)

// raceIterations is how many fresh fixtures each race test re-runs. Each
// iteration replants the whole hierarchy (cleanup cascades), so this stays
// small; the value only widens the odds of exercising both interleavings.
const raceIterations = 5

func TestService_RideLockRacesRoundClose(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, scheduleSvc := newTestService()

	for i := 0; i < raceIterations; i++ {
		t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
			roundID, homeID, awayID := seedFixture(ctx, t)
			closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
			ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

			if _, err := pool.Exec(ctx, `INSERT INTO schedule.rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
				t.Fatalf("insert round 2: %v", err)
			}
			destination := insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

			// The close command needs the round row as its callers supply
			// it: read the stored facts, and make the transition decision
			// the handler makes after ValidateRoundUpdate — the update
			// carries the target status, not the stored one.
			var round schedule.Round
			if err := pool.QueryRow(ctx, `
				SELECT id, season_id, number, status, version
				FROM schedule.rounds WHERE id = $1
			`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
				t.Fatalf("read round: %v", err)
			}
			if round.Status != schedule.RoundOpen {
				t.Fatalf("fixture round status = %q, want open", round.Status)
			}
			round.Status = schedule.RoundClosed

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
				_, closeErr = scheduleSvc.Rounds.Close(ctx, round)
			}()
			wg.Wait()

			// Nothing in either fixture or schedule refuses the close: its
			// only match is closed, and undecided rides no longer block it
			// (the gate half relaxed in 000014 — the settlement is the
			// wired handler's, right after the flip commits).
			if closeErr != nil {
				t.Fatalf("Close() = %v, want nil", closeErr)
			}

			got, err := NewStore(gamePool).Rides.Get(ctx, ride.ID)
			if err != nil {
				t.Fatalf("Get() = %v, want nil", err)
			}

			switch {
			case lockErr == nil:
				// The continuation committed before the close: the ride is
				// locked on the destination, outside the closing round's
				// settlement set (which is keyed on the round's own matches).
				if got.State != RideLocked || got.MatchID != destination {
					t.Errorf("lock won but state/match = %q/%d, want locked/%d", got.State, got.MatchID, destination)
				}
			case errors.Is(lockErr, ErrRoundNotOpen):
				// The close committed first: the continuation's round gate
				// re-read the committed row and refused it. The undecided
				// ride settled right after, from the RoundCompleted payload
				// (the settlement runs synchronously inside Close's
				// post-commit publish — eventual, but in-process).
				if got.State != RideUnlocked {
					t.Errorf("close won but state = %q, want unlocked (settled from the event)", got.State)
				}
			default:
				t.Fatalf("RideLock() = %v, want nil or ErrRoundNotOpen", lockErr)
			}

			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM schedule.rounds WHERE id = $1`, roundID).Scan(&status); err != nil {
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
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 88, 79, 'closed', now() + $5::interval, now() + $5::interval + interval '2 hours')
		RETURNING id
	`, seasonID, roundID, homeID, awayID, lead).Scan(&id)
	if err != nil {
		t.Fatalf("insert settled future match: %v", err)
	}

	return id
}

// waitUntilInsertBlocked polls pg_stat_activity until the given goroutine's
// ride command is actually waiting on the round row's lock. The scripted
// close-first race relies on this ordering: without the wait, a slow
// goroutine could see the committed close and be refused for the mundane
// reason, proving nothing about the lock. The blocked statement is the
// SchedulePort's round gate — schedule.round_open_guard (the FOR SHARE
// lives inside schedule's definer function since 000018) — the ride write
// runs only after the gate returns.
func waitUntilInsertBlocked(ctx context.Context, t *testing.T) {
	t.Helper()

	for i := 0; i < 100; i++ {
		var waiting int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%round_open_guard%'
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
	svc, scheduleSvc := newTestService()

	t.Run("close wins the write instant: the guarded insert is refused", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertSettledFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		// The close command's status flip, held uncommitted: updateRound
		// takes the round row's lock exactly like Rounds.Close does. Both
		// gates pass — the match is settled, no ride exists yet. (Context
		// schemas and roles: the flip is a schedule write, so the tx runs on
		// the schedule pool — the ride's FOR SHARE guard still blocks on this
		// tx's row lock; same DB, same serialization.)
		tx, err := schedulePool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin close tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		var round schedule.Round
		if err := pool.QueryRow(ctx, `
			SELECT id, season_id, number, status, version
			FROM schedule.rounds WHERE id = $1
		`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
			t.Fatalf("read round: %v", err)
		}
		round.Status = schedule.RoundClosed
		// The close flip itself (the guarded version UPDATE updateRound
		// runs), inline so the tx's lock-taking matches the store's write
		// shape without reaching into schedule's unexported internals.
		if _, err := tx.Exec(ctx, `
			UPDATE schedule.rounds SET number = $1, status = $2, version = version + 1
			WHERE id = $3 AND version = $4
		`, round.Number, round.Status, round.ID, round.Version); err != nil {
			t.Fatalf("uncommitted close flip: %v", err)
		}

		// RidePhase's read sees the round open (the close is uncommitted),
		// but the guarded insert blocks on its FOR SHARE until the close
		// commits, then re-evaluates the WHERE against the committed row.
		// The idempotency key is iteration-unique: each iteration replants
		// the fixture (cleanup cascades), but idempotency_keys survives it.
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
			}, rideToken("create-vs-close", "POST /v1/rides"), idempotentMarshal)
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
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM game.rides WHERE match_id = $1`, matchID).Scan(&n); err != nil {
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

		create, err := svc.RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}, rideToken("create-wins", "POST /v1/rides"), idempotentMarshal)
		if err != nil {
			t.Fatalf("RideCreate() = %v, want nil", err)
		}
		// The stored presentation is the marshaled ride: the created id
		// comes out of the response body.
		var created Ride
		if err := json.Unmarshal(create.Body, &created); err != nil {
			t.Fatalf("decode stored ride: %v", err)
		}
		if created.ID == 0 {
			t.Fatal("created id = 0, want assigned")
		}

		// The close commits after the create: the ride is locked on the
		// round's settled match, so the RoundCompleted settlement settles
		// it from the payload result (the backstop) and then sweeps it with
		// the undecided ones — the same settle-then-force-resolve the old
		// close transaction performed in one tx.
		var round schedule.Round
		if err := pool.QueryRow(ctx, `
			SELECT id, season_id, number, status, version
			FROM schedule.rounds WHERE id = $1
		`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
			t.Fatalf("read round: %v", err)
		}
		round.Status = schedule.RoundClosed
		if _, err := scheduleSvc.Rounds.Close(ctx, round); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}

		got, err := NewStore(gamePool).Rides.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		// The created ride is version 1: settled (+1) then swept (+1).
		if got.State != RideUnlocked || got.Version != 3 {
			t.Errorf("state/version = %q/%d, want unlocked/3 (settled from the result, then swept)",
				got.State, got.Version)
		}
	})
}

func TestService_RideBurnRacesExactlyOnce(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

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

			got, err := NewStore(gamePool).Rides.Get(ctx, ride.ID)
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

// TestService_RideCommandsRaceOnOneRide pins the loser's error when two
// *different* commands race from one shared snapshot (the classify task's
// mechanism, Phase 04.1): both commands Get the same won_pending row and
// pass their domain check — DecisionPhase, won_pending burns and unlocks —
// then serialize on the guarded write. The loser's CAS re-evaluates against
// the winner's commit and matches 0 rows on the version/state predicates.
//
// That cause must answer ErrInvalidTransition (the invalid-state 409: the
// claimed pre-state no longer exists), never ErrRoundNotOpen — the round is
// open for the whole fixture, so a round-refusal here would be a lie that
// sends the client away from a retryable conflict. The domain read side of
// this same race (Get landing after the winner's commit) already answers
// ErrInvalidTransition via canTransition; the write side now agrees.
func TestService_RideCommandsRaceOnOneRide(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

	for i := 0; i < raceIterations; i++ {
		t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
			roundID, homeID, awayID := seedFixture(ctx, t)
			closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
			ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

			// Two independent requests (distinct keys), one snapshot: the
			// loser must never land its write, and must lose with the
			// invalid-transition 409, not the round-refusal 409.
			var (
				wg   sync.WaitGroup
				errs = make([]error, 2)
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, errs[0] = svc.RideBurn(ctx, ride.ID, rideToken(fmt.Sprintf("race-burn-vs-unlock-a-%d", i), "POST /v1/rides/:id/burn"), idempotentMarshal)
			}()
			go func() {
				defer wg.Done()
				_, errs[1] = svc.RideUnlock(ctx, ride.ID, rideToken(fmt.Sprintf("race-burn-vs-unlock-b-%d", i), "POST /v1/rides/:id/unlock"), idempotentMarshal)
			}()
			wg.Wait()

			winners := 0
			for _, err := range errs {
				switch {
				case err == nil:
					winners++
				case errors.Is(err, ErrInvalidTransition):
					// The loser: either its domain read saw the winner's
					// commit (burned/unlocked is outside won_pending's
					// transitions) or its guarded write lost the CAS and
					// classifyUpdateRefusal named the cause.
				default:
					t.Fatalf("ride command = %v, want nil or ErrInvalidTransition", err)
				}
			}
			if winners != 1 {
				t.Fatalf("winners = %d (errs: %v), want exactly 1", winners, errs)
			}

			// The settled state: exactly one decision landed — burned or
			// unlocked, at exactly one version bump. A loser that wrote
			// anything shows up here as a double bump.
			got, err := NewStore(gamePool).Rides.Get(ctx, ride.ID)
			if err != nil {
				t.Fatalf("Get() = %v, want nil", err)
			}
			switch got.State {
			case RideBurned, RideUnlocked:
			default:
				t.Errorf("state = %q, want burned or unlocked", got.State)
			}
			if got.Version != ride.Version+1 {
				t.Errorf("version = %d, want %d (exactly one write landed)", got.Version, ride.Version+1)
			}
			if got.MatchID != closedMatch {
				t.Errorf("match = %d, want %d (neither command re-points)", got.MatchID, closedMatch)
			}
		})
	}
}

// TestService_MatchResolveRaces pins the resolve script's two Phase 04
// races (the deferred note's excursion: "two concurrent resolutions ...
// idempotent or a clean conflict"):
//
//	two resolutions:  the version guard inside ResolveTx serializes them —
//	                  exactly one settles match and rides (the winner's
//	                  post-commit publish), the loser reads 0 rows and
//	                  reports the clean ErrEditConflict (not the ambiguous
//	                  ErrRecordNotFound);
//	resolve vs close: the close can only pass rounds_close_gate
//	                  (round_close_has_unclosed_matches) after the
//	                  resolution committed; whichever publishes first
//	                  settles the ride, and the other handler is a no-op —
//	                  the outcome set is (resolve nil, close nil) or
//	                  (resolve nil, close ErrRecordInUse); resolve never
//	                  loses.
//
// As everywhere in this file: both legal outcomes are accepted, no
// interleaving order is pinned, and the assertions hold under either.
func TestResolveMatchRaces(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, scheduleSvc := newTestService()

	// startedMatch builds the resolve fixture: the match must satisfy
	// ResolveTx's contended facts honestly (in_progress, started in the
	// past, un-ended) — the WHERE is the concurrency contract under test.
	startedMatch := func(ctx context.Context, t *testing.T, roundID, homeID, awayID int) (int, int) {
		t.Helper()
		fixtureMatch := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "-30 minutes")
		scoreStartedMatch(ctx, t, fixtureMatch)

		var version int
		if err := pool.QueryRow(ctx, `SELECT version FROM schedule.matches WHERE id = $1`, fixtureMatch).Scan(&version); err != nil {
			t.Fatalf("read match version: %v", err)
		}
		return fixtureMatch, version
	}

	t.Run("two concurrent resolutions settle exactly once", func(t *testing.T) {
		for i := 0; i < raceIterations; i++ {
			t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
				roundID, homeID, awayID := seedFixture(ctx, t)
				matchID, version := startedMatch(ctx, t, roundID, homeID, awayID)
				r1 := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)
				r2 := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

				// Two independent resolutions, same caller version: both pass
				// their snapshot check, then serialize on the match row lock.
				var (
					wg   sync.WaitGroup
					errs = make([]error, 2)
				)
				wg.Add(2)
				for j := range errs {
					go func() {
						defer wg.Done()
						_, errs[j] = scheduleSvc.ResolveMatch(ctx, matchID, resolveScore(70, 69), time.Now().Add(-2*time.Hour), version)
					}()
				}
				wg.Wait()

				winners := 0
				for _, err := range errs {
					switch {
					case err == nil:
						winners++
					case errors.Is(err, store.ErrEditConflict):
						// The loser: blocked ResolveTx re-evaluated its WHERE
						// after the winner's commit and matched 0 rows — the
						// EvalPlanQual re-check refused the stale version, and
						// classifyResolveRefusal named it a clean 409.
					default:
						t.Fatalf("ResolveMatch() = %v, want nil or ErrEditConflict", err)
					}
				}
				if winners != 1 {
					t.Fatalf("winners = %d (errs: %v), want exactly 1", winners, errs)
				}

				// The settled states, and nothing more: home won_pending,
				// away lost, both at exactly one version bump. A loser that
				// had written anything would show up here as a double bump.
				for _, tc := range []struct {
					ride  Ride
					state RideState
				}{{r1, RideWonPending}, {r2, RideLost}} {
					got, err := svc.Store.Rides.Get(ctx, tc.ride.ID)
					if err != nil {
						t.Fatalf("Get(%d): %v", tc.ride.ID, err)
					}
					if got.State != tc.state || got.Version != 2 {
						t.Errorf("ride %d state/version = %q/%d, want %q/2", got.ID, got.State, got.Version, tc.state)
					}
				}

				var status string
				if err := pool.QueryRow(ctx, `SELECT status FROM schedule.matches WHERE id = $1`, matchID).Scan(&status); err != nil {
					t.Fatalf("read match status: %v", err)
				}
				if status != "closed" {
					t.Errorf("match status = %q, want closed", status)
				}
			})
		}
	})

	t.Run("resolution beats the close on an unsettled round", func(t *testing.T) {
		for i := 0; i < raceIterations; i++ {
			t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
				roundID, homeID, awayID := seedFixture(ctx, t)
				matchID, _ := startedMatch(ctx, t, roundID, homeID, awayID)
				ride := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)

				// The close command's round row, as its callers supply it:
				// read the stored facts, carry the target status like the
				// handler does.
				var round schedule.Round
				if err := pool.QueryRow(ctx, `
					SELECT id, season_id, number, status, version
					FROM schedule.rounds WHERE id = $1
				`, roundID).Scan(&round.ID, &round.SeasonID, &round.Number, &round.Status, &round.Version); err != nil {
					t.Fatalf("read round: %v", err)
				}
				if round.Status != schedule.RoundOpen {
					t.Fatalf("fixture round status = %q, want open", round.Status)
				}
				round.Status = schedule.RoundClosed

				var (
					wg         sync.WaitGroup
					resolveErr error
					closeErr   error
				)
				wg.Add(2)
				go func() {
					defer wg.Done()
					_, resolveErr = scheduleSvc.ResolveMatch(ctx, matchID, resolveScore(70, 69), time.Now().Add(-2*time.Hour), 1)
				}()
				go func() {
					defer wg.Done()
					_, closeErr = scheduleSvc.Rounds.Close(ctx, round)
				}()
				wg.Wait()

				// The resolve cannot lose: for the close to pass
				// rounds_close_gate at its flip, the match must be closed
				// committed — which only the resolve did, together with the
				// ride settlement. Close loses to the gate (ErrRecordInUse)
				// or runs after.
				if resolveErr != nil {
					t.Fatalf("ResolveMatch() = %v, want nil", resolveErr)
				}
				// The two legal interleavings also differ in the ride's final
				// state: a won ride settles TO won_pending (undecided until
				// the player claims or burns it). When the close wins the
				// flip, its RoundCompleted settlement force-resolves the
				// undecided ride (unlock) — synchronously, inside Close's
				// post-commit publish. When the close loses the gate
				// (unclosed match at its flip), the player keeps the
				// decision and the ride stays where the resolve left it.
				var wantState RideState
				var wantVersion int
				switch {
				case closeErr == nil:
					wantState, wantVersion = RideUnlocked, 3
				case errors.Is(closeErr, store.ErrRecordInUse):
					wantState, wantVersion = RideWonPending, 2
				default:
					t.Fatalf("Close() = %v, want nil or ErrRecordInUse", closeErr)
				}

				var matchStatus string
				if err := pool.QueryRow(ctx, `SELECT status FROM schedule.matches WHERE id = $1`, matchID).Scan(&matchStatus); err != nil {
					t.Fatalf("read match status: %v", err)
				}
				if matchStatus != "closed" {
					t.Errorf("match status = %q, want closed", matchStatus)
				}

				got, err := NewStore(gamePool).Rides.Get(ctx, ride.ID)
				if err != nil {
					t.Fatalf("Get() = %v, want nil", err)
				}
				if got.State != wantState || got.Version != wantVersion {
					t.Errorf("ride state/version = %q/%d, want %q/%d", got.State, got.Version, wantState, wantVersion)
				}

				// The forbidden shape: a closed round that still owns an
				// unresolved (locked or undecided) ride. Both handlers run
				// synchronously inside their publishers' post-commit
				// publishes, so by the time both goroutines return, a
				// committed close has settled everything its payload named.
				var violations int
				if err := pool.QueryRow(ctx, `
					SELECT count(*)
					FROM game.rides rd
					JOIN schedule.matches m ON m.id = rd.match_id
					WHERE m.round_id = $1 AND rd.state IN ('locked', 'won_pending')
				`, roundID).Scan(&violations); err != nil {
					t.Fatalf("count unresolved rides: %v", err)
				}
				var roundStatus string
				if err := pool.QueryRow(ctx, `SELECT status FROM schedule.rounds WHERE id = $1`, roundID).Scan(&roundStatus); err != nil {
					t.Fatalf("read round status: %v", err)
				}
				if roundStatus == "closed" && violations > 0 {
					t.Errorf("round closed with %d unresolved rides (the third shape)", violations)
				}
			})
		}
	})
}

// TestSettlementHandlersSerializeOnTheRound pins the settlement handlers'
// serialization (the per-round advisory lock in runEvent) at the exact
// interleaving that used to strand a ride: a resolve's and a close's
// publishes firing at the same moment, the RoundCompleted settlement
// reading the ride still locked and losing the locked→won_pending CAS to
// the MatchResultRecorded settlement running beside it. The loser rolled
// back WITH its idempotency claim, the bus dropped the failed delivery,
// and the ride stayed won_pending under a closed round — the third shape
// — with no redelivery until Phase 07.
//
// The resolve settlement is emulated on a manually held transaction (the
// handler itself cannot be paused mid-flight): the same advisory lock and
// the same guarded arrival its tx performs at the contended instant. The
// delivery must wait behind it and converge on the committed result —
// sweep the freshly won_pending ride unlocked — whichever order the two
// settlements run in. Under the serialization the ride lands unlocked/3
// regardless of timing; without it, the delivery loses the CAS and
// surfaces ErrInvalidTransition (the stranded shape).
func TestSettlementHandlersSerializeOnTheRound(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

	roundID, homeID, awayID := seedFixture(ctx, t)
	matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "-30 minutes")
	scoreStartedMatch(ctx, t, matchID)
	ride := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)

	// The resolve settlement, held mid-flight on its own transaction:
	// its serialization and its guarded arrival, nothing committed yet.
	txResolve, err := gamePool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the resolve settlement tx: %v", err)
	}
	defer func() { _ = txResolve.Rollback(context.Background()) }()

	if _, err := txResolve.Exec(ctx, settlementLockSQL, settlementLockClass, roundID); err != nil {
		t.Fatalf("settlement lock: %v", err)
	}
	if _, err := svc.Store.Rides.ArriveTx(ctx, txResolve, ride.ID, RideLocked, RideWonPending, ride.Version); err != nil {
		t.Fatalf("held resolve arrival: %v", err)
	}

	// The close's RoundCompleted delivery, while the resolve settlement
	// is uncommitted: with the serialization it claims its event and
	// blocks on the round lock; without it, it reads the ride still
	// locked and races the held arrival.
	event := schedule.RoundCompleted{
		EventID:  fmt.Sprintf("evt-settle-serialize-%d", time.Now().UnixNano()),
		RoundID:  roundID,
		SeasonID: 1,
		IsFinal:  false,
		Results: []schedule.MatchResult{
			{
				MatchID:      matchID,
				SeasonID:     1,
				RoundID:      roundID,
				HomeTeamID:   homeID,
				AwayTeamID:   awayID,
				Score:        resolveScore(70, 69),
				Odds:         schedule.Odds{Home: 1.75, Away: 2.20},
				WinnerTeamID: winnerOf(70, 69, homeID, awayID),
				EndedAt:      time.Now().Add(-2 * time.Hour),
			},
		},
	}
	delivered := make(chan error, 1)
	go func() { delivered <- svc.OnRoundCompleted(ctx, event) }()

	// Let the delivery reach its lock (or, unserialized, its contended
	// read) before the held settlement commits: the overlap this test
	// pins. The convergence assertion below holds under any timing with
	// the serialization — the sleep only keeps the unfixed shape
	// reproducible.
	time.Sleep(100 * time.Millisecond)

	if err := txResolve.Commit(ctx); err != nil {
		t.Fatalf("commit the resolve settlement: %v", err)
	}

	select {
	case err := <-delivered:
		if err != nil {
			t.Fatalf("OnRoundCompleted() = %v, want nil (the delivery must wait, not lose the CAS)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnRoundCompleted did not return; still blocked behind the held settlement?")
	}

	got, err := svc.Store.Rides.Get(ctx, ride.ID)
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}
	if got.State != RideUnlocked || got.Version != 3 {
		t.Errorf("ride state/version = %q/%d, want %q/3 (settled by the resolve, force-resolved by the close)", got.State, got.Version, RideUnlocked)
	}
	if !got.Acc.IsZero() {
		t.Errorf("acc = %s, want 0 (the force-resolve releases the bonus)", got.Acc)
	}
}
