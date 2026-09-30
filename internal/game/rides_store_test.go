package game

// Store-level tests for RideStore: Insert / Get / GetAll / Update against
// the real rides table, plus the DB gates the store must translate —
// rides_state_gate (P0001 → ErrInvalidTransition, ADR-020) and the FK
// parents (23503 → ErrRecordNotFound, the PlayerStore convention).
//
// Same harness as the service tests (constraints_test.go: pool, seed,
// cleanup, requireDB). Rides are planted with RideStore.Insert, which
// stores caller-supplied state — the creation contract belongs to
// ride.Create — and the state gate is BEFORE UPDATE, so INSERTs plant
// won_pending/burned rows freely.

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
		match2 := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "31 days")
		player1 := plantPlayer(ctx, t, homeID)
		player2 := plantPlayer(ctx, t, awayID)

		rs := NewStore(pool).Rides

		r1, err := rs.Insert(ctx, Ride{PlayerID: player1, TeamID: homeID, MatchID: match1, State: RideLocked, TokensLocked: decimal.NewFromInt(100), BaseAtLock: decimal.NewFromInt(95)})
		if err != nil {
			t.Fatalf("plant r1: %v", err)
		}
		r2, err := rs.Insert(ctx, Ride{PlayerID: player1, TeamID: homeID, MatchID: match2, State: RideWonPending, TokensLocked: decimal.NewFromInt(100), BaseAtLock: decimal.NewFromInt(95), Acc: decimal.NewFromInt(90), Streak: 1})
		if err != nil {
			t.Fatalf("plant r2: %v", err)
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
