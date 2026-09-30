package game

// RoundStore.Close integration tests: the round-close transaction's two
// halves — the auto-resolution of undecided won_pending rides and the
// status flip — plus the gates that referee them (000008), and the
// RESTRICT FK the rides table points at matches with (ADR-018).
//
// Same harness as the other store tests (constraints_test.go: pool, seed,
// cleanup, requireDB). Rides are planted by direct INSERT via
// RideStore.Insert — no producer exists until Phase 04 (ResolveMatch);
// the state gate is BEFORE UPDATE, so INSERTs plant won_pending freely.
//
// Close's statement order is load-bearing and the tests mirror it:
// resolution runs first, the flip is where the rounds gates evaluate, and
// a failure after the resolution must leave zero committed changes — that
// is the phase's done-when (spec: "A failure injected halfway through any
// major operation leaves the database exactly as it was before the
// operation started").

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/store"
)

// closeRoundSQL is the direct-writer close statement the gate-backstop
// test probes with. RoundStore.Close runs its resolution before the flip,
// so the gate refusal requires a writer that skips it.
const closeRoundSQL = `UPDATE rounds SET status = 'closed' WHERE id = $1`

// settleRound closes every match of the round directly (the shape the API
// round tests use): starts_at moves into the past so the score
// constraints and the freeze gate accept it, and the status transition
// stamps ended_at.
func settleRound(ctx context.Context, t *testing.T, roundID int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		UPDATE matches SET starts_at = now() - interval '3 hours',
			status = 'closed', home_score = 2, away_score = 1
		WHERE round_id = $1`, roundID); err != nil {
		t.Fatalf("settle round matches: %v", err)
	}
}

// countRidesInState counts the rides of a round (reached through matches —
// no round_id on rides, ADR-018) in a given state.
func countRidesInState(ctx context.Context, t *testing.T, roundID int, state RideState) int {
	t.Helper()

	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM rides rd
		JOIN matches m ON m.id = rd.match_id
		WHERE m.round_id = $1 AND rd.state = $2`, roundID, state).Scan(&n)
	if err != nil {
		t.Fatalf("count rides in state %q: %v", state, err)
	}

	return n
}

// TestMatchDeleteRestricted pins the ADR-018 rule: a match with rides on
// it must refuse deletion (RESTRICT), not cascade the rides away — the
// ride is live player money, not decoration on the match row. The store
// maps the refusal (23001/23503) to ErrRecordInUse, like TeamStore.Delete.
func TestMatchDeleteRestricted(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	roundID, homeID, awayID := seedFixture(ctx, t)
	matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
	plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)

	err := NewStore(pool).Matches.Delete(ctx, matchID, 1)

	if !errors.Is(err, store.ErrRecordInUse) {
		t.Fatalf("Delete() = %v, want ErrRecordInUse (RESTRICT FK, ADR-018)", err)
	}

	// The refusal actually refused: the match row survives.
	if _, err := NewStore(pool).Matches.Get(ctx, matchID); err != nil {
		t.Fatalf("Get() after refused delete = %v, want nil", err)
	}
}

// seedClosedRound hands back an open round whose matches are all settled
// (rounds_close_gate's precondition) with one undecided won_pending ride
// planted on its match, and the ride's owning player.
func seedClosedRound(ctx context.Context, t *testing.T) (int, int, Ride) {
	t.Helper()

	cleanup(ctx, t)
	// seedFixture re-seeds the base hierarchy but does not return the
	// season id; the close path needs it, so read it off the planted round.
	seasonID := 0
	roundID, homeID, awayID := seedFixture(ctx, t)
	if err := pool.QueryRow(ctx, `SELECT season_id FROM rounds WHERE id = $1`, roundID).Scan(&seasonID); err != nil {
		t.Fatalf("fetch season id: %v", err)
	}
	// The fixture round has no matches yet: plant one, then settle it —
	// rounds_close_gate requires every match of the closing round closed.
	insertFutureMatch(ctx, t, seasonID, roundID, homeID, awayID, "3 days")
	settleRound(ctx, t, roundID)

	var matchID int
	if err := pool.QueryRow(ctx, `SELECT id FROM matches WHERE round_id = $1 LIMIT 1`, roundID).Scan(&matchID); err != nil {
		t.Fatalf("fetch match id: %v", err)
	}

	ride := plantRide(ctx, t, matchID, homeID, RideWonPending, decimal.NewFromInt(90), 1)

	return seasonID, roundID, ride
}

// closeableRound hands back the stored round with the close transition
// applied — the handler contract: Close receives the round as currently
// stored plus the requested target status, and updateRound's version
// check still guards the write.
func closeableRound(ctx context.Context, t *testing.T, roundID int) Round {
	t.Helper()

	round, err := NewStore(pool).Rounds.Get(ctx, roundID)
	if err != nil {
		t.Fatalf("round Get() = %v, want nil", err)
	}
	round.Status = RoundClosed

	return round
}

func TestRoundStore_Close(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("auto-unlocks undecided rides and closes the round (ADR-022)", func(t *testing.T) {
		_, roundID, ride := seedClosedRound(ctx, t)

		got, err := NewStore(pool).Rounds.Close(ctx, closeableRound(ctx, t, roundID))
		if err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if got.Status != RoundClosed {
			t.Errorf("status = %q, want closed", got.Status)
		}

		persisted, err := NewStore(pool).Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideUnlocked {
			t.Errorf("state = %q, want unlocked (auto-unlock)", persisted.State)
		}
		// rides_state_gate refuses non-zero acc on the unlocked arrival, so
		// a close that left acc set would have aborted the transaction here.
		if !persisted.Acc.IsZero() {
			t.Errorf("acc = %s, want zero", persisted.Acc)
		}
		if persisted.Version != ride.Version+1 {
			t.Errorf("version = %d, want %d", persisted.Version, ride.Version+1)
		}
	})

	t.Run("player decision before the close survives it (concurrent-writer case)", func(t *testing.T) {
		_, roundID, ride := seedClosedRound(ctx, t)

		// A player who decided to burn before the close: the close must not
		// clobber that decision — the close's conditional UPDATE carries
		// state = 'won_pending' in its WHERE, so a row that has already moved
		// on is simply skipped: the player decision lands first, the close
		// re-evaluates the WHERE under READ COMMITTED and skips it. The
		// decision is issued phase-free through RideStore.Update (the
		// service command would re-derive DecisionPhase; the store half is
		// what this case exercises).
		decision := ride
		decision.State = RideBurned
		if _, err := NewStore(pool).Rides.Update(ctx, decision); err != nil {
			t.Fatalf("player burn before close: %v", err)
		}

		if n := countRidesInState(ctx, t, roundID, RideBurned); n != 1 {
			t.Fatalf("burned rides = %d, want 1", n)
		}

		if _, err := NewStore(pool).Rounds.Close(ctx, closeableRound(ctx, t, roundID)); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}

		wonPending := countRidesInState(ctx, t, roundID, RideWonPending)
		unlocked := countRidesInState(ctx, t, roundID, RideUnlocked)
		if wonPending != 0 || unlocked != 0 {
			t.Errorf("rides after close: won_pending=%d unlocked=%d, want 0/0 (the burned ride must stay burned)",
				wonPending, unlocked)
		}
	})

	t.Run("gate refuses a direct-writer close over unresolved rides", func(t *testing.T) {
		_, roundID, _ := seedClosedRound(ctx, t)

		// The writer that skips the resolution step: rounds_close_gate
		// (000008) is the backstop for exactly this — P0001, not a silent
		// close over won_pending rides.
		_, err := pool.Exec(ctx, closeRoundSQL, roundID)
		if err == nil {
			t.Fatal("direct close over won_pending rides = nil, want refusal")
		}
		if !store.IsTriggerViolation(err) {
			t.Fatalf("direct close = %v, want the rounds_close_gate P0001", err)
		}

		// The refusal actually refused.
		round, err := NewStore(pool).Rounds.Get(ctx, roundID)
		if err != nil {
			t.Fatalf("round Get() = %v, want nil", err)
		}
		if round.Status != RoundOpen {
			t.Errorf("round status = %q, want open", round.Status)
		}
	})
}

// TestRoundStore_Close_MidTxFailureLeavesNoTrace is the phase done-when:
// the status flip fails after the ride resolution succeeded, and nothing
// of the transaction survives — ride state, acc and version all read back
// as if the close never ran.
//
// The failure is not injected by hacking the store: the fixture aims the
// close at a round whose parent season is created, so the ride resolution
// succeeds and then rounds_progress_gate refuses the status flip
// (round_progress_season_not_live) — the last statement fails, exactly the
// halfway-failure shape the spec asks to test.
func TestRoundStore_Close_MidTxFailureLeavesNoTrace(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	seasonID, roundID, ride := seedClosedRound(ctx, t)

	// Close the season — a legal forward transition (the freeze gate only
	// refuses created/open regressions) — so rounds_progress_gate refuses
	// the round's status flip: the season must be live while a round
	// closes.
	if _, err := pool.Exec(ctx, `UPDATE seasons SET status = 'closed' WHERE id = $1`, seasonID); err != nil {
		t.Fatalf("deactivate season: %v", err)
	}

	_, err := NewStore(pool).Rounds.Close(ctx, closeableRound(ctx, t, roundID))
	if err == nil {
		t.Fatal("Close() = nil, want the season-not-live refusal")
	}
	// updateRound maps the rounds-gate P0001 to ErrRecordInUse (409),
	// ADR-007/008 convention.
	if !errors.Is(err, store.ErrRecordInUse) {
		t.Fatalf("Close() = %v, want ErrRecordInUse", err)
	}

	// The ride is untouched: state, acc and version all read back as
	// planted. Any committed resolution would show up here.
	persisted, err := NewStore(pool).Rides.Get(ctx, ride.ID)
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}
	if persisted.State != RideWonPending {
		t.Errorf("state = %q, want won_pending (transaction rolled back)", persisted.State)
	}
	if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
		t.Errorf("acc = %s, want the planted 90 (rollback restored it)", persisted.Acc)
	}
	if persisted.Version != ride.Version {
		t.Errorf("version = %d, want %d (no bump — the resolution rolled back)",
			persisted.Version, ride.Version)
	}

	// And the round is still open.
	round, err := NewStore(pool).Rounds.Get(ctx, roundID)
	if err != nil {
		t.Fatalf("round Get() = %v, want nil", err)
	}
	if round.Status != RoundOpen {
		t.Errorf("round status = %q, want open", round.Status)
	}
}

// TestRoundStore_Close_ProvesPostgresRollsBack runs Close's exact
// statement sequence inside a manual transaction that fails at the flip,
// purely to pin the DB-level mechanism the store's shape relies on: the
// resolution UPDATE is visible inside its own transaction before the flip
// fails, and vanishes with the rollback. Unlike the mid-tx-failure test
// above (which exercises RoundStore itself), this one is the raw
// Postgres-semantics probe behind the pattern.
func TestRoundStore_Close_ProvesPostgresRollsBack(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	seasonID, roundID, ride := seedClosedRound(ctx, t)

	if _, err := pool.Exec(ctx, `UPDATE seasons SET status = 'closed' WHERE id = $1`, seasonID); err != nil {
		t.Fatalf("deactivate season: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, rideAutoUnlockSQL, roundID); err != nil {
		t.Fatalf("resolution exec inside manual tx: %v", err)
	}

	// Observable within the tx: the resolution has taken effect on the
	// connection's own snapshot.
	if n := countRidesInStateInTx(ctx, t, tx, roundID, RideUnlocked); n != 1 {
		t.Fatalf("in-tx unlocked count = %d, want 1", n)
	}

	// The flip under a dead season aborts the statement (and the tx).
	if _, err := tx.Exec(ctx, `UPDATE rounds SET status = 'closed', version = version + 1 WHERE id = $1 AND version = 1`, roundID); err == nil {
		t.Fatal("flip in dead-season tx = nil, want the progress-gate refusal")
	} else if !store.IsTriggerViolation(err) {
		t.Fatalf("flip error = %v, want P0001", err)
	}

	if err := tx.Commit(ctx); err == nil {
		t.Fatal("Commit() after an aborted statement = nil, want an error")
	}

	// Outside the tx: nothing survived.
	persisted, err := NewStore(pool).Rides.Get(ctx, ride.ID)
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}
	if persisted.State != RideWonPending || persisted.Version != ride.Version {
		t.Errorf("ride after rollback = state %q version %d, want untouched won_pending/%d",
			persisted.State, persisted.Version, ride.Version)
	}
}

// countRidesInStateInTx is countRidesInState against a transaction — the
// in-tx visibility probe for the rollback semantics test.
func countRidesInStateInTx(ctx context.Context, t *testing.T, tx pgx.Tx, roundID int, state RideState) int {
	t.Helper()

	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM rides rd
		JOIN matches m ON m.id = rd.match_id
		WHERE m.round_id = $1 AND rd.state = $2`, roundID, state).Scan(&n)
	if err != nil {
		t.Fatalf("count rides in tx: %v", err)
	}

	return n
}
