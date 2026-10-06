package game

// Store-level tests for RideStore: Insert / Get / GetAll / Update against
// the real rides table, plus the DB gates the store must translate —
// rides_state_gate (P0001 → ErrInvalidTransition, ADR-020) and the FK
// parents (23503 → ErrRecordNotFound, the PlayerStore convention).
//
// Same harness as the service tests (constraints_test.go: pool, seed,
// cleanup, requireDB). Since 000012 rides are planted locked and walked
// to the requested state through the gate (service_test.go plantRide) —
// the walk needs the fixture match's scores to agree with the planted
// state, so decided-state plants go on closed matches.

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/store"
)

func rideFilters(sort string) store.Filters {
	return store.Filters{
		Page:         1,
		PageSize:     10,
		Sort:         sort,
		SortSafelist: []string{"id", "-id", "player_id", "-player_id", "state", "-state"},
	}
}

func TestRideStore_InsertAndGet(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("insert and get round-trips every column", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		rs := NewStore(pool).Rides
		ride, err := rs.Insert(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       homeID,
			MatchID:      matchID,
			State:        RideLocked,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromFloat(95.50),
			Acc:          decimal.NewFromInt(7),
			Streak:       3,
		})
		if err != nil {
			t.Fatalf("Insert() = %v, want nil", err)
		}
		if ride.ID == 0 {
			t.Fatal("id = 0, want assigned")
		}
		if ride.Version != 1 {
			t.Fatalf("version = %d, want 1", ride.Version)
		}
		if ride.CreatedAt.IsZero() {
			t.Fatal("created_at not assigned")
		}

		got, err := rs.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}

		// Every stored column round-trips: the store owns the row shape, so
		// any missed scan column surfaces here as a zero field.
		if got.ID != ride.ID || got.PlayerID != playerID || got.TeamID != homeID || got.MatchID != matchID {
			t.Errorf("identity columns: got %+v, want player %d team %d match %d", got, playerID, homeID, matchID)
		}
		if got.State != RideLocked {
			t.Errorf("state = %q, want %q", got.State, RideLocked)
		}
		if !got.TokensLocked.Equal(decimal.NewFromInt(100)) {
			t.Errorf("tokens_locked = %s, want 100", got.TokensLocked)
		}
		if !got.BaseAtLock.Equal(decimal.NewFromFloat(95.50)) {
			t.Errorf("base_at_lock = %s, want 95.5", got.BaseAtLock)
		}
		if !got.Acc.Equal(decimal.NewFromInt(7)) || got.Streak != 3 {
			t.Errorf("acc/streak = %s/%d, want 7/3", got.Acc, got.Streak)
		}
		if got.Version != 1 {
			t.Errorf("version = %d, want 1", got.Version)
		}
	})

	t.Run("get unknown ride is ErrRecordNotFound", func(t *testing.T) {
		seedFixture(ctx, t)

		_, err := NewStore(pool).Rides.Get(ctx, 999)

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("Get(999) = %v, want ErrRecordNotFound", err)
		}
	})

	t.Run("insert with unknown player is ErrRecordNotFound", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")

		_, err := NewStore(pool).Rides.Insert(ctx, Ride{
			PlayerID:     999, // no such player
			TeamID:       homeID,
			MatchID:      matchID,
			State:        RideLocked,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

		if !errors.Is(err, store.ErrRecordNotFound) {
			t.Fatalf("Insert() = %v, want ErrRecordNotFound (FK 23503 mapping)", err)
		}
	})

	t.Run("insert of a non-locked state is ErrInvalidTransition (000012)", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		// rides_state_insert_gate: rides are born locked (ADR-019) — a
		// direct INSERT that skips the transition graph reaches no other
		// state, the same hole the BEFORE UPDATE gate could not close.
		rs := NewStore(pool).Rides
		_, err := rs.Insert(ctx, Ride{
			PlayerID:     playerID,
			TeamID:       homeID,
			MatchID:      matchID,
			State:        RideWonPending,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		})

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Insert() = %v, want ErrInvalidTransition (000012 gate mapping)", err)
		}

		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM rides WHERE match_id = $1`, matchID).Scan(&count); err != nil {
			t.Fatalf("count rides: %v", err)
		}
		if count != 0 {
			t.Errorf("rides for match %d = %d, want 0 (the refusal must not have written)", matchID, count)
		}
	})
}

// TestRideStore_GetAll pins the filter/sort/pagination contract of the list
// query. Every filter is exercised both ways (matching and not matching) —
// a filter that silently matches everything, or binds the wrong placeholder,
// only shows up when the negative case is asserted too.
func TestRideStore_GetAll(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("filters, sort and pagination", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		match1 := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		// match2 is decided-with-score: r2 plants won_pending on it through
		// the gate (000011 result agreement), which needs scores agreeing
		// with homeID (the fixture closed match ends 88:79, home wins).
		match2 := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		player1 := plantPlayer(ctx, t, homeID)
		player2 := plantPlayer(ctx, t, awayID)

		rs := NewStore(pool).Rides

		r1, err := rs.Insert(ctx, Ride{PlayerID: player1, TeamID: homeID, MatchID: match1, State: RideLocked, TokensLocked: decimal.NewFromInt(100), BaseAtLock: decimal.NewFromInt(95)})
		if err != nil {
			t.Fatalf("plant r1: %v", err)
		}
		r2, err := rs.Insert(ctx, Ride{PlayerID: player1, TeamID: homeID, MatchID: match2, State: RideLocked, TokensLocked: decimal.NewFromInt(100), BaseAtLock: decimal.NewFromInt(95), Acc: decimal.NewFromInt(90), Streak: 1})
		if err != nil {
			t.Fatalf("plant r2: %v", err)
		}
		// The gated walk to won_pending — r2 must hold a second state for
		// the state-filter cases, and INSERT cannot plant it directly.
		if _, err := pool.Exec(ctx, `UPDATE rides SET state = 'won_pending' WHERE id = $1`, r2.ID); err != nil {
			t.Fatalf("walk r2 to won_pending: %v", err)
		}
		r3, err := rs.Insert(ctx, Ride{PlayerID: player2, TeamID: awayID, MatchID: match1, State: RideLocked, TokensLocked: decimal.NewFromInt(50), BaseAtLock: decimal.NewFromInt(95)})
		if err != nil {
			t.Fatalf("plant r3: %v", err)
		}

		tests := []struct {
			name      string
			query     func(ctx context.Context) ([]Ride, store.Metadata, error)
			wantCount int
			wantFirst int // id of the first returned row (sort-sensitive); 0 = don't check
		}{
			{
				name: "no filters returns all three",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, 0, "", rideFilters("id"))
				},
				wantCount: 3,
			},
			{
				name: "filter by player",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, player1, 0, 0, "", rideFilters("id"))
				},
				wantCount: 2,
			},
			{
				name: "player filter matching nothing",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 999, 0, 0, "", rideFilters("id"))
				},
				wantCount: 0,
			},
			{
				name: "filter by team",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, homeID, 0, "", rideFilters("id"))
				},
				wantCount: 2,
			},
			{
				name: "team filter matching nothing",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 999, 0, "", rideFilters("id"))
				},
				wantCount: 0,
			},
			{
				name: "filter by match",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, match1, "", rideFilters("id"))
				},
				wantCount: 2,
			},
			{
				name: "match filter matching nothing",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, 999, "", rideFilters("id"))
				},
				wantCount: 0,
			},
			{
				name: "filter by state",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, 0, RideLocked, rideFilters("id"))
				},
				wantCount: 2,
			},
			{
				name: "state filter matching nothing",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, 0, RideBurned, rideFilters("id"))
				},
				wantCount: 0,
			},
			{
				// Combined filters intersect: player 1 has one locked ride.
				name: "combined player and state filters",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, player1, 0, 0, RideLocked, rideFilters("id"))
				},
				wantCount: 1,
				wantFirst: r1.ID,
			},
			{
				name: "combined player and match filters",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, player1, 0, match2, "", rideFilters("id"))
				},
				wantCount: 1,
				wantFirst: r2.ID,
			},
			{
				name: "sort descending puts the newest ride first",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, 0, 0, 0, 0, "", rideFilters("-id"))
				},
				wantCount: 3,
				wantFirst: r3.ID,
			},
			{
				name: "single id filter returns exactly that ride",
				query: func(ctx context.Context) ([]Ride, store.Metadata, error) {
					return rs.GetAll(ctx, r2.ID, 0, 0, 0, "", rideFilters("id"))
				},
				wantCount: 1,
				wantFirst: r2.ID,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rides, metadata, err := tt.query(ctx)
				if err != nil {
					t.Fatalf("GetAll() = %v, want nil", err)
				}
				if len(rides) != tt.wantCount {
					t.Fatalf("got %d rides, want %d (rows: %+v)", len(rides), tt.wantCount, rides)
				}
				if tt.wantCount > 0 && metadata.TotalRecords != tt.wantCount {
					t.Errorf("total_records = %d, want %d", metadata.TotalRecords, tt.wantCount)
				}
				if tt.wantFirst != 0 && rides[0].ID != tt.wantFirst {
					t.Errorf("first row id = %d, want %d", rides[0].ID, tt.wantFirst)
				}
			})
		}
	})

	t.Run("pagination windows and metadata", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		rs := NewStore(pool).Rides
		for range 3 {
			if _, err := rs.Insert(ctx, Ride{PlayerID: playerID, TeamID: homeID, MatchID: matchID, State: RideLocked, TokensLocked: decimal.NewFromInt(100), BaseAtLock: decimal.NewFromInt(95)}); err != nil {
				t.Fatalf("plant ride: %v", err)
			}
		}

		t.Run("page 1 with page_size 2", func(t *testing.T) {
			f := rideFilters("id")
			f.PageSize = 2

			rides, metadata, err := rs.GetAll(ctx, 0, 0, 0, 0, "", f)
			if err != nil {
				t.Fatalf("GetAll() = %v, want nil", err)
			}
			if len(rides) != 2 {
				t.Fatalf("got %d rides, want 2", len(rides))
			}
			if metadata.TotalRecords != 3 || metadata.LastPage != 2 || metadata.CurrentPage != 1 {
				t.Errorf("metadata = %+v, want total 3 / last 2 / current 1", metadata)
			}
		})

		t.Run("page 2 carries the remaining row", func(t *testing.T) {
			f := rideFilters("id")
			f.PageSize = 2
			f.Page = 2

			rides, metadata, err := rs.GetAll(ctx, 0, 0, 0, 0, "", f)
			if err != nil {
				t.Fatalf("GetAll() = %v, want nil", err)
			}
			if len(rides) != 1 {
				t.Fatalf("got %d rides, want 1", len(rides))
			}
			if metadata.CurrentPage != 2 {
				t.Errorf("current_page = %d, want 2", metadata.CurrentPage)
			}
		})

		// count(*) OVER() materializes per returned row: an offset past all
		// matching rows yields no rows and empty metadata (pinned in the API
		// list tests too — this is where the shape comes from).
		t.Run("page beyond the last is empty with empty metadata", func(t *testing.T) {
			f := rideFilters("id")
			f.Page = 99

			rides, metadata, err := rs.GetAll(ctx, 0, 0, 0, 0, "", f)
			if err != nil {
				t.Fatalf("GetAll() = %v, want nil", err)
			}
			if len(rides) != 0 {
				t.Fatalf("got %d rides, want 0", len(rides))
			}
			if metadata != (store.Metadata{}) {
				t.Errorf("metadata = %+v, want zero", metadata)
			}
		})
	})
}

// TestRideStore_Update covers the optimistic-lock write and every
// rides_state_gate refusal the store must translate into
// ErrInvalidTransition (ADR-020). The gate is phase-free — phase checks
// live in the command methods — so the store-level cases use no phase at
// all: whatever the trigger accepts, the store accepts.
func TestRideStore_Update(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("happy path persists the fields and bumps the version", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		ride.State = RideBurned
		ride.Streak = 2

		got, err := NewStore(pool).Rides.Update(ctx, ride)
		if err != nil {
			t.Fatalf("Update() = %v, want nil", err)
		}
		if got.Version != ride.Version+1 {
			t.Errorf("version = %d, want %d", got.Version, ride.Version+1)
		}

		persisted, err := NewStore(pool).Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideBurned || persisted.Streak != 2 {
			t.Errorf("persisted state/streak = %q/%d, want burned/2", persisted.State, persisted.Streak)
		}
		if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
			t.Errorf("acc = %s, want unchanged 90 (burn keeps acc)", persisted.Acc)
		}
	})

	t.Run("stale version is ErrEditConflict", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		ride.Version = ride.Version - 1 // somebody else already wrote

		_, err := NewStore(pool).Rides.Update(ctx, ride)

		if !errors.Is(err, store.ErrEditConflict) {
			t.Fatalf("Update() = %v, want ErrEditConflict", err)
		}
	})

	t.Run("illegal transition is ErrInvalidTransition (gate P0001)", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideLocked, decimal.Zero, 0)

		// locked → burned is outside the transition graph (ADR-016). The
		// domain command would refuse it too; this case proves the DB gate
		// refuses it independently and the store maps P0001 to
		// ErrInvalidTransition — the app/DB agreement is tested, not trusted.
		ride.State = RideBurned

		_, err := NewStore(pool).Rides.Update(ctx, ride)

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Update() = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("result disagreement is ErrInvalidTransition (000011)", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		// Away team planted: the fixture closed match ends 88:79 (home
		// wins), so a won_pending arrival for the away ride disagrees.
		ride := plantRide(ctx, t, closedMatch, awayID, RideLocked, decimal.Zero, 0)

		// The transition itself passes check 3 (locked → won_pending is in
		// the graph). What refuses is the result-agreement check alone: the
		// DB re-derives the winner from the match row (ADR-018) instead of
		// trusting the writer — a rogue UPDATE cannot plant a fake win.
		ride.State = RideWonPending

		_, err := NewStore(pool).Rides.Update(ctx, ride)

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Update() = %v, want ErrInvalidTransition (result disagreement)", err)
		}
	})

	t.Run("match re-point outside continuation is refused", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		match1 := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		match2 := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "31 days")
		ride := plantRide(ctx, t, match1, homeID, RideLocked, decimal.Zero, 0)

		// ADR-018: match_id writes only on won_pending → locked.
		ride.MatchID = match2

		_, err := NewStore(pool).Rides.Update(ctx, ride)

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Update() = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("continuation re-points the match", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		match1 := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		match2 := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		ride := plantRide(ctx, t, match1, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		ride.State = RideLocked
		ride.MatchID = match2
		ride.Streak = 2

		if _, err := NewStore(pool).Rides.Update(ctx, ride); err != nil {
			t.Fatalf("Update() = %v, want nil", err)
		}

		persisted, err := NewStore(pool).Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideLocked || persisted.MatchID != match2 {
			t.Errorf("persisted state/match = %q/%d, want locked/%d", persisted.State, persisted.MatchID, match2)
		}
	})

	t.Run("terminal ride rejects a state change but accepts a rewrite", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideBurned, decimal.NewFromInt(90), 1)
		rs := NewStore(pool).Rides

		t.Run("state change", func(t *testing.T) {
			frozen := ride
			frozen.State = RideLost

			_, err := rs.Update(ctx, frozen)

			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("Update() = %v, want ErrInvalidTransition", err)
			}
		})

		// The store's Update shape writes the full mutable column set even
		// when nothing changed (idempotent re-send); the gate must let a
		// state-preserving rewrite of a terminal ride through.
		t.Run("state-preserving rewrite", func(t *testing.T) {
			same := ride

			if _, err := rs.Update(ctx, same); err != nil {
				t.Fatalf("Update() = %v, want nil", err)
			}
		})
	})

	t.Run("unlocked arrival with non-zero acc is refused", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 1)

		// A writer that flips to unlocked but forgets to zero acc: the gate
		// is the second line of defense behind ride.Unlock (which zeroes it).
		ride.State = RideUnlocked

		_, err := NewStore(pool).Rides.Update(ctx, ride)

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Update() = %v, want ErrInvalidTransition", err)
		}
	})
}

// guardedWrite runs UpdateGuardedTx on its own transaction — the store-level
// equivalent of a ride command's write step (the idempotency claim lives one
// level up, in the service). Errors roll the tx back and surface to the
// caller's assertions.
func guardedWrite(ctx context.Context, t *testing.T, rs *RideStore, ride Ride, destination int) (Ride, error) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	got, err := rs.UpdateGuardedTx(ctx, tx, ride, destination)
	if err != nil {
		_ = tx.Rollback(context.Background())
		return Ride{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Ride{}, err
	}

	return got, nil
}

// TestRideStore_UpdateGuardedTx covers the DecisionPhase commands' guarded
// write (Phase 04 TOCTOU sweep): one UPDATE whose WHERE re-checks every
// fact the advisory pipeline relied on, so a concurrent round close or
// match end cannot land between check and write. Two deliberate choices
// the cases pin:
//
//   - any 0-rows outcome is ErrRoundNotOpen, the round-refusal 409 the
//     advisory path already answers — including a stale version, which
//     plain Update reports as ErrEditConflict. A won_pending ride whose
//     row moved was almost always moved by the round-close transaction
//     (ADR-022 auto-unlock), so the round is the truthful client-facing
//     reason; ErrEditConflict would name the wrong conflict.
//   - a rides_state_gate P0001 stays ErrInvalidTransition, exactly like
//     Update — the trigger remains the transition graph's DB authority.
func TestRideStore_UpdateGuardedTx(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	rs := &NewStore(pool).Rides

	t.Run("continuation re-points onto the destination under the guard", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		if _, err := pool.Exec(ctx, `INSERT INTO rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
			t.Fatalf("insert round 2: %v", err)
		}
		destination := insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		// The service shape: the domain command mutates the ride first
		// (ADR-016), then the guarded write re-verifies the pre-mutation
		// facts — state won_pending, own round open, destination live.
		ride.State = RideLocked
		ride.MatchID = destination
		ride.Streak++

		got, err := guardedWrite(ctx, t, rs, ride, destination)
		if err != nil {
			t.Fatalf("UpdateGuardedTx() = %v, want nil", err)
		}
		if got.Version != ride.Version+1 {
			t.Errorf("version = %d, want %d", got.Version, ride.Version+1)
		}

		persisted, err := rs.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideLocked || persisted.MatchID != destination {
			t.Errorf("state/match = %q/%d, want locked/%d", persisted.State, persisted.MatchID, destination)
		}
		if persisted.Streak != 3 {
			t.Errorf("streak = %d, want 3", persisted.Streak)
		}
		if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
			t.Errorf("acc = %s, want unchanged 90 (lock is not a payout)", persisted.Acc)
		}
	})

	t.Run("burn through the guard leaves match and acc alone", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		ride.State = RideBurned

		if _, err := guardedWrite(ctx, t, rs, ride, 0); err != nil {
			t.Fatalf("UpdateGuardedTx() = %v, want nil", err)
		}

		persisted, err := rs.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideBurned || persisted.MatchID != closedMatch {
			t.Errorf("state/match = %q/%d, want burned/%d", persisted.State, persisted.MatchID, closedMatch)
		}
		if !persisted.Acc.Equal(decimal.NewFromInt(90)) {
			t.Errorf("acc = %s, want unchanged 90 (burn keeps acc)", persisted.Acc)
		}
	})

	t.Run("stale version is the round-refusal 409, not an edit conflict", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		// Bump the version without moving state (a state-preserving
		// rewrite is legal to the gate): the version predicate alone now
		// refuses the guarded write, and the mapped error must be the
		// round sentinel Update maps to ErrEditConflict.
		if _, err := pool.Exec(ctx, `UPDATE rides SET bonus_acc = bonus_acc + 0.0001, version = version + 1 WHERE id = $1`, ride.ID); err != nil {
			t.Fatalf("bump version: %v", err)
		}

		ride.State = RideBurned
		_, err := guardedWrite(ctx, t, rs, ride, 0)

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrRoundNotOpen", err)
		}
	})

	t.Run("a ride already decided refuses the second decision", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		first := ride
		first.State = RideBurned
		if _, err := guardedWrite(ctx, t, rs, first, 0); err != nil {
			t.Fatalf("first UpdateGuardedTx() = %v, want nil", err)
		}

		// A second player command (or a retry) holding the pre-decision
		// snapshot: the state predicate refuses it — the decision is
		// re-checked at the write instant, not only in the domain.
		_, err := guardedWrite(ctx, t, rs, ride, 0)

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrRoundNotOpen", err)
		}
	})

	t.Run("a closed round refuses even a fresh ride row", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")

		// Close the round while no rides exist (the close gate passes: its
		// only match is closed, no won_pending rides), then plant a
		// won_pending ride directly — INSERTs sit outside the BEFORE
		// UPDATE state gate, the same trick the other store tests use.
		if _, err := pool.Exec(ctx, `UPDATE rounds SET status = 'closed' WHERE id = $1`, roundID); err != nil {
			t.Fatalf("close round: %v", err)
		}
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		// The version column cannot see the round's status: a plain
		// version-checked UPDATE would write here, leaving a live decision
		// inside a closed round — the exact defect the sweep fixes.
		ride.State = RideBurned
		_, err := guardedWrite(ctx, t, rs, ride, 0)

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrRoundNotOpen", err)
		}

		persisted, err := rs.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideWonPending || persisted.Version != ride.Version {
			t.Errorf("persisted state/version = %q/%d, want won_pending/%d (nothing written)",
				persisted.State, persisted.Version, ride.Version)
		}
	})

	t.Run("a destination ended after the pick is refused", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		if _, err := pool.Exec(ctx, `INSERT INTO rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
			t.Fatalf("insert round 2: %v", err)
		}
		// The continuation destination: a started (in_progress) match for
		// the same team in another round — the race target must be a match
		// the world event can legally settle (starts_at in the past; the
		// ended-at trigger stamps now(), which matches_ended_at_check
		// requires to be after starts_at).
		var destination int
		if err := pool.QueryRow(ctx, `
			INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at)
			VALUES (1, 2, $1, $2, 1.75, 2.20, 10, 9, 'in_progress', now() - interval '30 minutes')
			RETURNING id
		`, homeID, awayID).Scan(&destination); err != nil {
			t.Fatalf("insert started destination: %v", err)
		}

		// The world-event shape: the chosen match settles between the pick
		// and the write (the ended-at trigger stamps the time).
		if _, err := pool.Exec(ctx, `UPDATE matches SET status = 'closed' WHERE id = $1`, destination); err != nil {
			t.Fatalf("settle destination: %v", err)
		}

		ride.State = RideLocked
		ride.MatchID = destination
		ride.Streak++
		_, err := guardedWrite(ctx, t, rs, ride, destination)

		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrRoundNotOpen", err)
		}

		persisted, err := rs.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		if persisted.State != RideWonPending || persisted.MatchID != closedMatch {
			t.Errorf("persisted state/match = %q/%d, want won_pending/%d (nothing written)",
				persisted.State, persisted.MatchID, closedMatch)
		}
	})

	t.Run("an unknown destination is refused without an FK hit", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		ride.State = RideLocked
		ride.MatchID = 424242
		ride.Streak++

		_, err := guardedWrite(ctx, t, rs, ride, 424242)

		// The destination EXISTS fails first, so the row is never written
		// and the FK never fires: one refusal shape for every stale pick.
		if !errors.Is(err, ErrRoundNotOpen) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrRoundNotOpen", err)
		}
	})

	t.Run("illegal transition is still the gate's P0001", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)

		// Every guard predicate passes, but won_pending → lost is outside
		// the transition graph (ADR-016): the DB gate refuses and the
		// mapping stays ErrInvalidTransition, as with Update.
		ride.State = RideLost
		_, err := guardedWrite(ctx, t, rs, ride, 0)

		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("UpdateGuardedTx() = %v, want ErrInvalidTransition", err)
		}
	})
}
