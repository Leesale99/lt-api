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
// opens DecisionPhase (the 1-hour lead/lag constants in round_phase.go).
//
// Ride storage is the real rides table (migration 000006), so rides are
// planted directly through RideStore.Insert (it stores whatever state the
// caller supplies — the creation contract is owned by ride.Create, not
// Insert — and the state gate is a BEFORE UPDATE trigger, so INSERTs are
// outside its reach). Cleanup cascades through the FK graph to the rides
// table, so ride rows vanish with the rest of the fixture; tests still
// capture IDs from return values rather than hard-code them.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

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

// plantRide stores a ride directly through the store with the given
// state/acc/streak, returning the stored row (with its assigned id).
// The ride needs a player row to satisfy the rides_player_id_fkey
// (rides RESTRICT-reference players, ADR-018).
func plantRide(ctx context.Context, t *testing.T, matchID, teamID int, state RideState, acc decimal.Decimal, streak int) Ride {
	t.Helper()

	ride, err := NewStore(pool).Rides.Insert(ctx, Ride{
		PlayerID:     plantPlayer(ctx, t, teamID),
		TeamID:       teamID,
		MatchID:      matchID,
		State:        state,
		TokensLocked: decimal.NewFromInt(100),
		BaseAtLock:   decimal.NewFromInt(100),
		Acc:          acc,
		Streak:       streak,
	})
	if err != nil {
		t.Fatalf("plant ride: %v", err)
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

		got, err := NewService(NewStore(pool)).RideCreate(ctx, newRide(playerID, matchID, homeID))

		if err != nil {
			t.Fatalf("RideCreate() = %v, want nil", err)
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

		if _, err := pool.Exec(ctx, `UPDATE rounds SET status = 'closed' WHERE id = $1`, roundID); err != nil {
			t.Fatalf("close round: %v", err)
		}

		_, err := NewService(NewStore(pool)).RideCreate(ctx, newRide(playerID, matchID, homeID))

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("RideCreate() = %v, want ErrRoundNotOpen", err)
		}
	})
}

func TestService_RideLock(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

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

		got, err := NewService(NewStore(pool)).RideLock(ctx, ride.ID)

		if err != nil {
			t.Fatalf("RideLock() = %v, want nil", err)
		}
		if got.State != RideLocked {
			t.Fatalf("state = %q, want %q", got.State, RideLocked)
		}
		if got.MatchID != nextMatch {
			t.Fatalf("match_id = %d, want %d (continuation re-points one FK)", got.MatchID, nextMatch)
		}
		if got.Streak != 3 {
			t.Fatalf("streak = %d, want 3", got.Streak)
		}
		// Lock is not a payout: acc survives into the next match.
		if !got.Acc.Equal(decimal.NewFromInt(90)) {
			t.Fatalf("acc = %s, want unchanged 90", got.Acc)
		}
	})

	t.Run("without a next match maps not-found to ErrNoNextMatch", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := NewService(NewStore(pool)).RideLock(ctx, ride.ID)

		if !errors.Is(err, ErrNoNextMatch) {
			t.Fatalf("RideLock() = %v, want ErrNoNextMatch", err)
		}
	})
}

func TestService_RideBurn(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("in decision phase burns and keeps acc", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		got, err := NewService(NewStore(pool)).RideBurn(ctx, ride.ID)

		if err != nil {
			t.Fatalf("RideBurn() = %v, want nil", err)
		}
		if got.State != RideBurned {
			t.Fatalf("state = %q, want %q", got.State, RideBurned)
		}
		// Payout is not the ride's concern; Burn leaves acc untouched.
		if !got.Acc.Equal(decimal.NewFromInt(90)) {
			t.Fatalf("acc = %s, want unchanged 90", got.Acc)
		}
	})
}

func TestService_RideUnlock(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("in decision phase unlocks and forfeits acc", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		got, err := NewService(NewStore(pool)).RideUnlock(ctx, ride.ID)

		if err != nil {
			t.Fatalf("RideUnlock() = %v, want nil", err)
		}
		if got.State != RideUnlocked {
			t.Fatalf("state = %q, want %q", got.State, RideUnlocked)
		}
		if !got.Acc.IsZero() {
			t.Fatalf("acc = %s, want 0 (unlock forfeits acc)", got.Acc)
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

		_, err := NewService(NewStore(pool)).RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       1,
			MatchID:      999, // no such match
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("RideCreate() = %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("unknown player is refused with ErrRecordNotFound", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")

		_, err := NewService(NewStore(pool)).RideCreate(ctx, Ride{
			PlayerID:     999, // no such player
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("RideCreate() = %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("unknown team is refused with ErrRecordNotFound", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		_, err := NewService(NewStore(pool)).RideCreate(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       999, // no such team
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

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

		_, err := NewService(NewStore(pool)).RideCreate(ctx, Ride{
			PlayerID:     7,
			TeamID:       homeID,
			MatchID:      matchID,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideCreate() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("lock during action phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		// A match starting far outside the action lead keeps the round in ActionPhase.
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := NewService(NewStore(pool)).RideLock(ctx, ride.ID)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideLock() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("burn during match phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 minutes")
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := NewService(NewStore(pool)).RideBurn(ctx, ride.ID)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideBurn() = %v, want ErrInvalidRoundPhase", err)
		}
	})

	t.Run("unlock during match phase", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 minutes")
		ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		_, err := NewService(NewStore(pool)).RideUnlock(ctx, ride.ID)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("RideUnlock() = %v, want ErrInvalidRoundPhase", err)
		}
	})
}
