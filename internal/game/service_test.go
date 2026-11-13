package game

// Service-level ride tests: the four ride use-cases (create, lock, burn,
// unlock) run end-to-end against the real stores and phase derivation at
// the game.Service boundary — the smallest scale that can contain a
// service-level bug. No HTTP (the handler is a thin error-mapping shell),
// no fakes: Service.Store is a concrete struct wired to pgx, so the
// boundary test runs where the boundary actually is (see constraints_test.go
// for the harness: pool, seed, cleanup, requireDB).
//
// The clock the service cannot inject — RidePhase calls time.Now() — is
// controlled the same way the DB-gate tests control time: matches are
// inserted with starts_at/ended_at relative to now(), far outside any test
// execution skew. A round whose first match starts in +30 days is
// ActionPhase; +30 minutes is MatchPhase; a closed match ended 2 hours ago
// opens DecisionPhase (the 1-hour lead/lag constants in schedule/round_phase.go).
//
// Ride storage is the real rides table (migration 000006), so rides are
// planted through RideStore.Insert plus a gated walk to the requested
// state (plantRide below) — since 000012 the INSERT gate admits locked
// only, so every other state is reached through the same transition rules
// any writer faces. Cleanup cascades through the FK graph to the rides
// table, so ride rows vanish with the rest of the fixture; tests still
// capture IDs from return values rather than hard-code them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/ledger"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/store"
)

// seedFixture re-seeds the base hierarchy (open round 1, teams 1 & 2) and
// returns their IDs. Call at the top of every subtest — cleanup first, as
// the gate tests do, so IDs restart from a known point.
func seedFixture(ctx context.Context, t *testing.T) (roundID, homeID, awayID int) {
	t.Helper()

	cleanup(ctx, t)
	// Season ID is fixed by the fixture and unused here: rides reach the
	// season only through the match, never directly.
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	_ = seasonID

	return roundID, homeID, awayID
}

// insertFutureMatch adds a pre-start match starting `lead` from now and
// returns its id. status is created; no score, no ended_at.
func insertFutureMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, lead string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 'created', now() + $5::interval)
		RETURNING id
	`, seasonID, roundID, homeID, awayID, lead).Scan(&id)
	if err != nil {
		t.Fatalf("insert future match: %v", err)
	}

	return id
}

// scoreStartedMatch flips a planted future match in_progress with a two-
// goal home win. The walk inside plantRide needs the ride's match to carry
// scores agreeing with the planted state (000011), and created matches
// cannot carry scores (matches_status_score_check), so phase-rejection
// fixtures that plant won_pending rides call this first. The in_progress
// status leaves ended_at nil — the round's phase window that Phase derives
// from is untouched, the tests' intended phase stays intact.
func scoreStartedMatch(ctx context.Context, t *testing.T, matchID int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		UPDATE matches SET status = 'in_progress', home_score = 70, away_score = 69 WHERE id = $1
	`, matchID); err != nil {
		t.Fatalf("score match %d: %v", matchID, err)
	}
}

// insertClosedMatch adds a finished match ended `agoAgo` before now
// (started 3 hours earlier) and returns its id. Closed-with-score is the
// only status/shape that satisfies phase derivation for DecisionPhase.
func insertClosedMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, ended string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 88, 79, 'closed', now() - $5::interval - interval '3 hours', now() - $5::interval)
		RETURNING id
	`, seasonID, roundID, homeID, awayID, ended).Scan(&id)
	if err != nil {
		t.Fatalf("insert closed match: %v", err)
	}

	return id
}

// plantPlayer inserts a player row on the fixture season (id restarts to 1
// on cleanup, the same fixture-fixed ID insertFutureMatch passes as its
// season_id) and returns the new player's id.
func plantPlayer(ctx context.Context, t *testing.T, teamID int) int {
	t.Helper()

	var playerID int
	err := pool.QueryRow(ctx, `
		INSERT INTO players (season_id, favorite_team_id, name)
		VALUES (1, $1, 'ride player')
		RETURNING id
	`, teamID).Scan(&playerID)
	if err != nil {
		t.Fatalf("plant player: %v", err)
	}

	return playerID
}

// plantRide stores a ride in the given state/acc/streak, returning the
// stored row (with its assigned id). The ride needs a player row to
// satisfy the rides_player_id_fkey (rides RESTRICT-reference players,
// ADR-018).
//
// Since 000012 the INSERT gate admits locked only, so the walk plants
// locked and transitions to the requested state through the same UPDATE
// gate every writer faces: locked → won_pending|lost directly,
// burned|unlocked via the won_pending intermediate (check 3). The
// result-agreement check (000011) applies to the walk — planting a state
// the fixture's scores disagree with fails loudly, which is the gate
// doing its job. The walk's own transitions are incidental to what tests
// arrange, so the final UPDATE resets version to 1: version assertions
// count from the planted state, not from the walk.
func plantRide(ctx context.Context, t *testing.T, matchID, teamID int, state RideState, acc decimal.Decimal, streak int) Ride {
	t.Helper()

	rs := NewStore(pool).Rides
	ride, err := rs.Insert(ctx, Ride{
		PlayerID:     plantPlayer(ctx, t, teamID),
		TeamID:       teamID,
		MatchID:      matchID,
		State:        RideLocked,
		TokensLocked: decimal.NewFromInt(100),
		BaseAtLock:   decimal.NewFromInt(100),
		Acc:          acc,
		Streak:       streak,
	})
	if err != nil {
		t.Fatalf("plant ride: %v", err)
	}

	if state != RideLocked {
		// burned/unlocked arrive only via won_pending (gate check 3).
		if state == RideBurned || state == RideUnlocked {
			if _, err := pool.Exec(ctx, `UPDATE rides SET state = 'won_pending' WHERE id = $1`, ride.ID); err != nil {
				t.Fatalf("plant ride walk (locked → won_pending): %v", err)
			}
		}

		// The arrival writes state and acc in one statement: the money-
		// state coupling reads NEW.bonus_acc on the lost/unlocked arrivals
		// — the declarative rides_money_state_coupling CHECK (000013) now,
		// which took the UPDATE gate's trigger check 4 in the sweep.
		if _, err := pool.Exec(ctx, `
			UPDATE rides SET state = $2, bonus_acc = $3, version = 1 WHERE id = $1
		`, ride.ID, state, acc); err != nil {
			t.Fatalf("plant ride walk (→ %s): %v", state, err)
		}

		ride.State = state
		ride.Acc = acc
		ride.Version = 1
	}

	return ride
}

func TestService_RideCreate(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	newRide := func(playerID, matchID, teamID int) Ride {
		// Caller-supplied domain-owned garbage on purpose: the ADR-019
		// ride.Create contract must overwrite it — the service adds no
		// state of its own.
		return Ride{
			PlayerID:     playerID,
			TeamID:       teamID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
			State:        RideBurned,
			Acc:          decimal.NewFromInt(5),
			Streak:       7,
		}
	}

	t.Run("during action phase inserts the initial-state contract ride", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		res, err := mustService().RideCreate(ctx, newRide(playerID, matchID, homeID), rideToken("create-happy", "POST /v1/rides"), idempotentMarshal)

		if err != nil {
			t.Fatalf("RideCreate() = %v, want nil", err)
		}
		// The stored presentation is the marshaled ride: decode it before
		// asserting the stored columns.
		var got Ride
		if err := json.Unmarshal(res.Body, &got); err != nil {
			t.Fatalf("decode stored ride: %v", err)
		}
		if got.ID == 0 {
			t.Fatal("id = 0, want assigned")
		}
		// created_at is stamped by the PostgreSQL server clock; this assertion
		// runs on the test-process clock. Two clocks, so a small positive delta
		// is host/sandbox skew, not a fabricated timestamp — tolerate it,
		// still fail on anything beyond that.
		if got.CreatedAt.After(time.Now().Add(2 * time.Second)) {
			t.Fatalf("created_at %v is more than 2s in the future", got.CreatedAt)
		}
		if got.State != RideLocked {
			t.Fatalf("state = %q, want %q (caller-supplied garbage must be discarded)", got.State, RideLocked)
		}
		if !got.Acc.IsZero() || got.Streak != 0 {
			t.Fatalf("acc/streak = %s/%d, want zeroed by the domain contract", got.Acc, got.Streak)
		}
		if got.PlayerID != playerID || got.TeamID != homeID || got.MatchID != matchID || !got.TokensLocked.Equal(decimal.NewFromInt(100)) || !got.BaseAtLock.Equal(decimal.NewFromInt(95)) {
			t.Fatal("caller-owned inputs were modified by the service")
		}
	})

	// No "wrong phase refused" case here: the phase guard lives in the
	// command methods and is unit-tested in rides_test.go. What the service
	// adds to the guard is the wiring claim — phase derived from real match
	// times, passed into the command — demonstrated once in
	// TestService_RideCommandPhaseRejection below.

	t.Run("on a closed round is refused with ErrRoundNotOpen", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		if _, err := pool.Exec(ctx, `UPDATE matches SET starts_at = now() - interval '3 hours',
			status = 'closed', home_score = 2, away_score = 1
			WHERE id = $1`, matchID); err != nil {
			t.Fatalf("settle match: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE rounds SET status = 'closed' WHERE id = $1`, roundID); err != nil {
			t.Fatalf("close round: %v", err)
		}

		_, err := mustService().RideCreate(ctx, newRide(playerID, matchID, homeID), rideToken("create-closed", "POST /v1/rides"), idempotentMarshal)

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("RideCreate() = %v, want ErrRoundNotOpen", err)
		}
	})
}

func TestService_RideLock(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

	t.Run("in decision phase continues onto the team's next match", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		// The continuation destination: a future match for the same team in
		// another round of the same season. The destination round stays
		// created — RideLock requires only the ride's own round to be in its
		// DecisionPhase, and NextForTeam filters r.status <> 'closed' — so
		// round 2 opens only after round 1 closes (rounds_progress_gate,
		// 000008), with the ride already re-pointed onto it.
		if _, err := pool.Exec(ctx, `INSERT INTO rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
			t.Fatalf("insert round 2: %v", err)
		}
		nextMatch := insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		_, err := svc.RideLock(ctx, ride.ID, rideToken("svc-lock-continues", "POST /v1/rides/:id/lock"), idempotentMarshal)
		if err != nil {
			t.Fatalf("RideLock() = %v, want nil", err)
		}

		// The command's return value is the stored response; the assertions
		// read the persisted row — the effect the command must have.
		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideLocked {
			t.Fatalf("state = %q, want %q", persisted.State, RideLocked)
		}
		if persisted.MatchID != nextMatch {
			t.Fatalf("match_id = %d, want %d (continuation re-points one FK)", persisted.MatchID, nextMatch)
		}
		if persisted.Streak != 3 {
			t.Fatalf("streak = %d, want 3", persisted.Streak)
		}
		// Lock is not a payout: acc survives into the next match.
		if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
			t.Fatalf("acc = %s, want unchanged 90", persisted.Acc)
		}
	})

	t.Run("without a next match maps not-found to ErrNoNextMatch", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := svc.RideLock(ctx, ride.ID, rideToken("svc-lock-no-next", "POST /v1/rides/:id/lock"), idempotentMarshal)

		if !errors.Is(err, ErrNoNextMatch) {
			t.Fatalf("RideLock() = %v, want ErrNoNextMatch", err)
		}
	})
}

func TestService_RideBurn(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

	t.Run("in decision phase burns and keeps acc", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := svc.RideBurn(ctx, ride.ID, rideToken("svc-burn", "POST /v1/rides/:id/burn"), idempotentMarshal)
		if err != nil {
			t.Fatalf("RideBurn() = %v, want nil", err)
		}

		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideBurned {
			t.Fatalf("state = %q, want %q", persisted.State, RideBurned)
		}
		// Payout is not the ride's concern; Burn leaves acc untouched.
		if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
			t.Fatalf("acc = %s, want unchanged 90", persisted.Acc)
		}
	})

	t.Run("retry with the same key replays without re-executing", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		token := rideToken("svc-burn-replay", "POST /v1/rides/:id/burn")
		first, err := svc.RideBurn(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("first RideBurn() error = %v", err)
		}
		second, err := svc.RideBurn(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("replayed RideBurn() error = %v", err)
		}
		if !bytes.Equal(first.Body, second.Body) {
			t.Errorf("replayed body differs:\nfirst:  %s\nsecond: %s", first.Body, second.Body)
		}

		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("read ride: %v", err)
		}
		if persisted.Version != ride.Version+1 {
			t.Errorf("version = %d, want %d (one transition total)", persisted.Version, ride.Version+1)
		}
	})
}

func TestService_RideUnlock(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc, _ := newTestService()

	t.Run("in decision phase unlocks and forfeits acc", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := svc.RideUnlock(ctx, ride.ID, rideToken("svc-unlock", "POST /v1/rides/:id/unlock"), idempotentMarshal)
		if err != nil {
			t.Fatalf("RideUnlock() = %v, want nil", err)
		}

		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideUnlocked {
			t.Fatalf("state = %q, want %q", persisted.State, RideUnlocked)
		}
		if !persisted.Acc.IsZero() {
			t.Fatalf("acc = %s, want 0 (unlock forfeits acc)", persisted.Acc)
		}
	})

	t.Run("retry with the same key replays without re-executing", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		token := rideToken("svc-unlock-replay", "POST /v1/rides/:id/unlock")
		first, err := svc.RideUnlock(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("first RideUnlock() error = %v", err)
		}
		second, err := svc.RideUnlock(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("replayed RideUnlock() error = %v", err)
		}
		if !bytes.Equal(first.Body, second.Body) {
			t.Errorf("replayed body differs:\nfirst:  %s\nsecond: %s", first.Body, second.Body)
		}

		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("read ride: %v", err)
		}
		if persisted.Version != ride.Version+1 {
			t.Errorf("version = %d, want %d (one transition total)", persisted.Version, ride.Version+1)
		}
	})
}

// TestService_RideCreateUnknownParents pins the store-side FK mapping at
// the service boundary: ValidateRide only checks positivity, so an unknown
// player/team/match survives the handler and must surface as
// ErrRecordNotFound from the store (→ 404), never a raw FK error (→ 500).
func TestService_RideCreateUnknownParents(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("unknown match is refused with ErrRecordNotFound", func(t *testing.T) {
		seedFixture(ctx, t)
		playerID := plantPlayer(ctx, t, 1)

		_, err := mustService().RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       1,
			MatchID:      999, // no such match
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}, rideToken("create-unknown-match", "POST /v1/rides"), idempotentMarshal)

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("RideCreate() = %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("unknown player is refused with ErrRecordNotFound", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")

		_, err := mustService().RideCreate(ctx, Ride{
			PlayerID:     999, // no such player
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}, rideToken("create-unknown-player", "POST /v1/rides"), idempotentMarshal)

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("RideCreate() = %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("unknown team is refused with ErrRecordNotFound", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		_, err := mustService().RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       999, // no such team
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}, rideToken("create-unknown-team", "POST /v1/rides"), idempotentMarshal)

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("RideCreate() = %v, want ErrRecordNotFound", err)
		}
	})
}

// for all four commands: the phase the service derives from real match
// times reaches the command methods, which refuse a command issued in the
// wrong phase. The guard itself and its per-phase matrix live at the domain
// level (rides_test.go) — here only the pipe from DB to command is tested.
func TestService_RideCommandPhaseRejection(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("create during match phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		// A match starting inside the 1-hour action lead puts the round in MatchPhase.
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 minutes")

		_, err := mustService().RideCreate(ctx, Ride{
			PlayerID:     7,
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}, rideToken("create-match-phase", "POST /v1/rides"), idempotentMarshal)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideCreate() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("lock during action phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		// A match starting far outside the action lead keeps the round in ActionPhase.
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		scoreStartedMatch(ctx, t, matchID)
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := mustService().RideLock(ctx, ride.ID, rideToken("svc-lock-action", "POST /v1/rides/:id/lock"), idempotentMarshal)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideLock() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("burn during match phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		// Negative lead: ResolveTx requires starts_at < now() — the match
		// must already be running, not merely in_progress.
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "-30 minutes")
		scoreStartedMatch(ctx, t, matchID)
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := mustService().RideBurn(ctx, ride.ID, rideToken("svc-burn-match-phase", "POST /v1/rides/:id/burn"), idempotentMarshal)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideBurn() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("unlock during match phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		// Negative lead: ResolveTx requires starts_at < now() — the match
		// must already be running, not merely in_progress.
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "-30 minutes")
		scoreStartedMatch(ctx, t, matchID)
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := mustService().RideUnlock(ctx, ride.ID, rideToken("svc-unlock-match-phase", "POST /v1/rides/:id/unlock"), idempotentMarshal)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideUnlock() = %v, want ErrInvalidRoundPhase", err)
		}
	})
}

// resolveScore builds the Score pointer pair the resolve payloads take; a
// helper keeps the call sites on one line.
func resolveScore(home, away int) schedule.Score {
	return schedule.Score{Home: &home, Away: &away}
}

// newTestService wires the full in-process stack the service tests run
// against (the same shape the composition root and the api harness wire):
// the real schedule service publishing on a bus, the real game service
// with its event handlers subscribed, the real ledger. The ride commands
// never publish, so the wiring is harmless for them and load-bearing for
// the settlement tests.
func newTestService() (*Service, *schedule.Service) {
	bus := eventbus.New()
	scheduleStore := schedule.NewStore(pool)
	scheduleSvc := schedule.NewService(scheduleStore, bus, nil)
	gameSvc := NewService(NewStore(pool), scheduleStore, ledger.NewStore())
	eventbus.Subscribe(bus, func(ctx context.Context, event schedule.MatchResultRecorded) error {
		return gameSvc.OnMatchResultRecorded(ctx, event)
	})
	eventbus.Subscribe(bus, func(ctx context.Context, event schedule.RoundCompleted) error {
		return gameSvc.OnRoundCompleted(ctx, event)
	})
	return gameSvc, scheduleSvc
}

// eventResult builds a MatchResultRecorded payload for the fixture match
// (home odds 1.75, away 2.20 — the insert helpers' shape), with the winner
// derived the way the publisher derives it (schedule.Match.Winner).
func eventResult(eventID string, matchID, roundID, seasonID, homeID, awayID, home, away int) schedule.MatchResultRecorded {
	score := resolveScore(home, away)
	match := schedule.Match{
		ID:         matchID,
		Status:     schedule.MatchClosed,
		HomeTeamID: homeID,
		AwayTeamID: awayID,
		Score:      score,
		Odds:       schedule.Odds{Home: 1.75, Away: 2.20},
	}
	return schedule.MatchResultRecorded{
		EventID:      eventID,
		MatchID:      matchID,
		RoundID:      roundID,
		SeasonID:     seasonID,
		HomeTeamID:   homeID,
		AwayTeamID:   awayID,
		Score:        score,
		Odds:         match.Odds,
		WinnerTeamID: match.Winner(),
	}
}

// settleRoundMatches closes every match of the round directly: starts_at
// moves into the past so the score constraints and the freeze gate accept
// it, and the status transition stamps ended_at.
func settleRoundMatches(ctx context.Context, t *testing.T, roundID int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		UPDATE matches SET starts_at = now() - interval '3 hours',
			status = 'closed', home_score = 2, away_score = 1
		WHERE round_id = $1`, roundID); err != nil {
		t.Fatalf("settle round matches: %v", err)
	}
}

// scoreClosedMatch plants a closed, scored match the settlement payloads
// hang off (started 3 hours before ending, same shape as the shared
// fixture) and returns (matchID, roundID, seasonID).
func scoreClosedMatch(ctx context.Context, t *testing.T, home, away int) (int, int, int) {
	t.Helper()

	cleanup(ctx, t)
	seasonID, roundID, homeID, awayID := seed(ctx, t)
	matchID := insertClosedMatchScored(ctx, t, seasonID, roundID, homeID, awayID, home, away)
	return matchID, roundID, seasonID
}

// TestService_OnMatchResultRecorded pins the match-result settlement
// (Phase C): rides settle from the event payload, the money halves move
// through the Ledger, replays are no-ops, and the DB stays the enforcement
// home (a payload disagreeing with the committed scores is refused by
// rides_state_gate, not applied).
func TestService_OnMatchResultRecorded(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("settles locked rides from the payload: win accrues, loss releases", func(t *testing.T) {
		gameSvc, _ := newTestService()
		matchID, roundID, seasonID := scoreClosedMatch(ctx, t, 3, 1)
		var homeID, awayID int
		if err := pool.QueryRow(ctx, `SELECT home_team_id, away_team_id FROM matches WHERE id = $1`, matchID).Scan(&homeID, &awayID); err != nil {
			t.Fatalf("read match: %v", err)
		}

		home := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.NewFromInt(40), 1)
		away := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.NewFromInt(90), 2)

		event := eventResult("evt-home-win", matchID, roundID, seasonID, homeID, awayID, 3, 1)
		if err := gameSvc.OnMatchResultRecorded(ctx, event); err != nil {
			t.Fatalf("OnMatchResultRecorded() = %v, want nil", err)
		}

		// Home won 3:1: the home ride is won_pending with the bonus
		// accrued at its team odds (1.75), one transition total; the away
		// ride is lost with its acc released to zero.
		gotHome, err := gameSvc.Store.Rides.Get(ctx, home.ID)
		if err != nil {
			t.Fatalf("Get(home): %v", err)
		}
		if gotHome.State != RideWonPending || gotHome.Version != 2 {
			t.Errorf("home ride = %s/v%d, want won_pending/v2", gotHome.State, gotHome.Version)
		}
		wantAcc := calculateBonus(decimal.NewFromFloat(1.75), gotHome.TokensLocked, decimal.NewFromInt(40), 1)
		if !gotHome.Acc.Equal(wantAcc) {
			t.Errorf("home ride acc = %s, want %s (the win accrues)", gotHome.Acc, wantAcc)
		}

		gotAway, err := gameSvc.Store.Rides.Get(ctx, away.ID)
		if err != nil {
			t.Fatalf("Get(away): %v", err)
		}
		if gotAway.State != RideLost || gotAway.Version != 2 {
			t.Errorf("away ride = %s/v%d, want lost/v2", gotAway.State, gotAway.Version)
		}
		if !gotAway.Acc.IsZero() {
			t.Errorf("away ride acc = %s, want 0 (the loss releases)", gotAway.Acc)
		}
	})

	t.Run("a draw lands every ride lost", func(t *testing.T) {
		gameSvc, _ := newTestService()
		matchID, roundID, seasonID := scoreClosedMatch(ctx, t, 2, 2)
		var homeID, awayID int
		if err := pool.QueryRow(ctx, `SELECT home_team_id, away_team_id FROM matches WHERE id = $1`, matchID).Scan(&homeID, &awayID); err != nil {
			t.Fatalf("read match: %v", err)
		}

		home := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)
		away := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

		event := eventResult("evt-draw", matchID, roundID, seasonID, homeID, awayID, 2, 2)
		if event.WinnerTeamID != nil {
			t.Fatal("draw payload must carry no winner")
		}
		if err := gameSvc.OnMatchResultRecorded(ctx, event); err != nil {
			t.Fatalf("OnMatchResultRecorded() = %v, want nil (draw resolves as all-lost)", err)
		}

		for _, ride := range []Ride{home, away} {
			got, err := gameSvc.Store.Rides.Get(ctx, ride.ID)
			if err != nil {
				t.Fatalf("Get(%d): %v", ride.ID, err)
			}
			if got.State != RideLost {
				t.Errorf("ride %d state = %q, want lost (a draw crowns no winner)", got.ID, got.State)
			}
		}
	})

	t.Run("a replayed event is a no-op success", func(t *testing.T) {
		gameSvc, _ := newTestService()
		matchID, roundID, seasonID := scoreClosedMatch(ctx, t, 3, 1)
		var homeID, awayID int
		if err := pool.QueryRow(ctx, `SELECT home_team_id, away_team_id FROM matches WHERE id = $1`, matchID).Scan(&homeID, &awayID); err != nil {
			t.Fatalf("read match: %v", err)
		}
		ride := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)

		event := eventResult("evt-replay", matchID, roundID, seasonID, homeID, awayID, 3, 1)
		if err := gameSvc.OnMatchResultRecorded(ctx, event); err != nil {
			t.Fatalf("first OnMatchResultRecorded() = %v, want nil", err)
		}

		// Redelivery (same EventID): the claim replays, nothing executes.
		if err := gameSvc.OnMatchResultRecorded(ctx, event); err != nil {
			t.Fatalf("replayed OnMatchResultRecorded() = %v, want nil (no-op success)", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideWonPending || got.Version != 2 {
			t.Errorf("ride = %s/v%d, want won_pending/v2 (settled exactly once)", got.State, got.Version)
		}
	})

	t.Run("a payload disagreeing with the committed scores is refused by the gate", func(t *testing.T) {
		gameSvc, _ := newTestService()
		matchID, roundID, seasonID := scoreClosedMatch(ctx, t, 3, 1)
		var homeID, awayID int
		if err := pool.QueryRow(ctx, `SELECT home_team_id, away_team_id FROM matches WHERE id = $1`, matchID).Scan(&homeID, &awayID); err != nil {
			t.Fatalf("read match: %v", err)
		}
		awayRide := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

		// A lying payload (away wins while the row says 3:1 home): the
		// derivation would land the away ride won_pending — check 5 of
		// rides_state_gate re-derives from the committed match row and
		// refuses. The DB is the enforcement home; the handler surfaces
		// the refusal, it never overrides it.
		event := eventResult("evt-lying", matchID, roundID, seasonID, homeID, awayID, 1, 3)
		err := gameSvc.OnMatchResultRecorded(ctx, event)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("OnMatchResultRecorded(lying payload) = %v, want ErrInvalidTransition", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, awayRide.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideLocked || got.Version != 1 {
			t.Errorf("ride = %s/v%d, want untouched locked/v1 (the tx rolled back)", got.State, got.Version)
		}
	})
}

// TestService_OnRoundCompleted pins the round settlement (Phase C eventual
// close): undecided rides force-resolve from the payload (unlock 1-37,
// burn the season's last round), still-locked rides settle from the
// payload results (the backstop for a lost MatchResultRecorded), and a
// replay is a no-op.
func TestService_OnRoundCompleted(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	// closedRoundFixture plants a settled round (all matches closed) and
	// returns the ids the round settlement needs.
	closedRoundFixture := func(ctx context.Context, t *testing.T, number int) (roundID, homeID, awayID, matchID int) {
		t.Helper()
		cleanup(ctx, t)
		seasonID, roundID, homeID, awayID := seed(ctx, t)
		if number != 1 {
			renumberRoundForFixture(ctx, t, roundID, number)
		}
		matchID = insertFutureMatch(ctx, t, seasonID, roundID, homeID, awayID, "3 days")
		settleRoundMatches(ctx, t, roundID)
		return roundID, homeID, awayID, matchID
	}

	roundEvent := func(eventID string, roundID, seasonID int, isFinal bool, matchID, homeID, awayID, home, away int) schedule.RoundCompleted {
		return schedule.RoundCompleted{
			EventID:  eventID,
			RoundID:  roundID,
			SeasonID: seasonID,
			IsFinal:  isFinal,
			Results: []schedule.MatchResult{
				{
					MatchID:      matchID,
					HomeTeamID:   homeID,
					AwayTeamID:   awayID,
					Score:        resolveScore(home, away),
					Odds:         schedule.Odds{Home: 1.75, Away: 2.20},
					WinnerTeamID: winnerOf(home, away, homeID, awayID),
				},
			},
		}
	}

	t.Run("undecided rides auto-unlock with the bonus released", func(t *testing.T) {
		gameSvc, _ := newTestService()
		roundID, homeID, awayID, matchID := closedRoundFixture(ctx, t, 1)
		seasonID := 1

		won := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		if err := gameSvc.OnRoundCompleted(ctx, roundEvent("evt-round-unlock", roundID, seasonID, false, matchID, homeID, awayID, 2, 1)); err != nil {
			t.Fatalf("OnRoundCompleted() = %v, want nil", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, won.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideUnlocked || got.Version != 2 {
			t.Errorf("ride = %s/v%d, want unlocked/v2 (one force-resolve)", got.State, got.Version)
		}
		if !got.Acc.IsZero() {
			t.Errorf("acc = %s, want 0 (the unlock releases the bonus)", got.Acc)
		}
	})

	t.Run("the season's last round force-burns undecided rides, acc kept", func(t *testing.T) {
		gameSvc, _ := newTestService()
		roundID, homeID, awayID, matchID := closedRoundFixture(ctx, t, 38)
		seasonID := 1

		won := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		if err := gameSvc.OnRoundCompleted(ctx, roundEvent("evt-round-burn", roundID, seasonID, true, matchID, homeID, awayID, 2, 1)); err != nil {
			t.Fatalf("OnRoundCompleted() = %v, want nil", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, won.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideBurned || got.Version != 2 {
			t.Errorf("ride = %s/v%d, want burned/v2 (the force-burn)", got.State, got.Version)
		}
		if !got.Acc.Equal(decimal.NewFromInt(90)) {
			t.Errorf("acc = %s, want 90 kept (burn is not a money-state arrival)", got.Acc)
		}
	})

	t.Run("still-locked rides settle from the payload results (backstop)", func(t *testing.T) {
		gameSvc, _ := newTestService()
		roundID, homeID, awayID, matchID := closedRoundFixture(ctx, t, 1)
		seasonID := 1

		// A locked ride whose result never reached the handler (the crash
		// gap): the round settlement settles it from the payload result and
		// then sweeps it with the undecided ones — the old close-tx
		// semantics (settle, then force-resolve).
		locked := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)

		if err := gameSvc.OnRoundCompleted(ctx, roundEvent("evt-round-backstop", roundID, seasonID, false, matchID, homeID, awayID, 2, 1)); err != nil {
			t.Fatalf("OnRoundCompleted() = %v, want nil", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, locked.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideUnlocked {
			t.Errorf("ride = %s, want unlocked (settled from the result, then swept)", got.State)
		}
		if !got.Acc.IsZero() {
			t.Errorf("acc = %s, want 0", got.Acc)
		}
		_ = awayID
	})

	t.Run("a replayed round event is a no-op success", func(t *testing.T) {
		gameSvc, _ := newTestService()
		roundID, homeID, awayID, matchID := closedRoundFixture(ctx, t, 1)
		seasonID := 1

		won := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		event := roundEvent("evt-round-replay", roundID, seasonID, false, matchID, homeID, awayID, 2, 1)
		if err := gameSvc.OnRoundCompleted(ctx, event); err != nil {
			t.Fatalf("first OnRoundCompleted() = %v, want nil", err)
		}
		if err := gameSvc.OnRoundCompleted(ctx, event); err != nil {
			t.Fatalf("replayed OnRoundCompleted() = %v, want nil (no-op success)", err)
		}

		got, err := gameSvc.Store.Rides.Get(ctx, won.ID)
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if got.State != RideUnlocked || got.Version != 2 {
			t.Errorf("ride = %s/v%d, want unlocked/v2 (settled exactly once)", got.State, got.Version)
		}
	})
}

// renumberRoundForFixture moves a live round to another number with the
// progress gate suspended: rounds_progress_gate requires an unbroken closed
// sequence (37 closed rounds before a live 38), which no realistic fixture
// builds. Fixture tooling only — production renumbering stays gated.
func renumberRoundForFixture(ctx context.Context, t *testing.T, roundID, number int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `ALTER TABLE rounds DISABLE TRIGGER rounds_progress_gate`); err != nil {
		t.Fatalf("suspend progress gate: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE rounds SET number = $2 WHERE id = $1`, roundID, number); err != nil {
		t.Fatalf("renumber round: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE rounds ENABLE TRIGGER rounds_progress_gate`); err != nil {
		t.Fatalf("restore progress gate: %v", err)
	}
}

// winnerOf derives the payload winner the publisher derives (a draw crowns
// no winner).
func winnerOf(home, away, homeID, awayID int) *int {
	switch {
	case home > away:
		return &homeID
	case away > home:
		return &awayID
	default:
		return nil
	}
}

// mustService returns just the game service of the wired stack, for tests
// that exercise only the ride commands (they never publish).
func mustService() *Service {
	svc, _ := newTestService()
	return svc
}
