package game

// Idempotency tests for the ride commands' shared engine (ADR-024),
// exercised through RideLock: claim-first single flight, stored-response
// replay, key-misuse conflict, and claim rollback on failure. Runs against
// the real idempotency_keys table (migration 000010).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/validator"
)

// idempotentMarshal is the minimal presentation callback: the status and
// body the command stores as its replayable response.
func idempotentMarshal(r Ride) (int, []byte, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return 0, nil, err
	}
	return 200, body, nil
}

// rideToken builds a valid token for a ride command endpoint: the hash must
// be SHA-256-sized (32 bytes), matching the idempotency_keys request_hash
// check.
func rideToken(key, endpoint string) IdempotencyToken {
	hash := sha256.Sum256([]byte(key))
	return IdempotencyToken{
		Key:      key,
		Endpoint: endpoint,
		Hash:     hash[:],
	}
}

// TestValidateIdempotencyToken pins the key rules next to the model: the
// same rule the DB's idempotency_keys_key_check enforces as the backstop
// (key <> '' AND octet_length(key) <= 255).
func TestValidateIdempotencyToken(t *testing.T) {
	valid := func() IdempotencyToken {
		return rideToken("replay-key", "POST /v1/rides/:id/lock")
	}

	tests := []struct {
		name     string
		mutate   func(t *IdempotencyToken)
		wantErrs map[string]string // field -> first error message; empty = valid
	}{
		{
			name:     "valid token",
			mutate:   func(t *IdempotencyToken) {},
			wantErrs: map[string]string{},
		},
		{
			name:     "empty key is rejected",
			mutate:   func(t *IdempotencyToken) { t.Key = "" },
			wantErrs: map[string]string{"idempotency_key": "must be provided"},
		},
		{
			name:     "key over 255 bytes is rejected",
			mutate:   func(t *IdempotencyToken) { t.Key = strings.Repeat("k", 256) },
			wantErrs: map[string]string{"idempotency_key": "must not be more than 255 bytes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := valid()
			tt.mutate(&token)

			v := validator.New()
			ValidateIdempotencyToken(v, token)

			if len(tt.wantErrs) == 0 {
				if !v.Valid() {
					t.Fatalf("ValidateIdempotencyToken() = %v, want no errors", v.Errors)
				}
				return
			}

			if v.Valid() {
				t.Fatal("ValidateIdempotencyToken() = valid, want errors")
			}
			for field, want := range tt.wantErrs {
				if got, ok := v.Errors[field]; !ok || got != want {
					t.Errorf("errors[%q] = %q (present: %v), want %q", field, got, ok, want)
				}
			}
		})
	}
}

// ensureRound2 creates the continuation round the fixtures lock into
// (same shape the round-close concurrency tests use).
func ensureRound2(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO rounds (season_id, number, status) VALUES (1, 2, 'created')`); err != nil {
		t.Fatalf("insert round 2: %v", err)
	}
}

func TestService_RideLockIdempotency(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	svc := NewService(NewStore(pool))

	t.Run("retry with the same key replays and re-executes nothing", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)
		ensureRound2(ctx, t)
		insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		token := rideToken("replay-key", "POST /v1/rides/:id/lock")

		first, err := svc.RideLock(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("first RideLock() error = %v", err)
		}
		second, err := svc.RideLock(ctx, ride.ID, token, idempotentMarshal)
		if err != nil {
			t.Fatalf("replayed RideLock() error = %v", err)
		}

		if !bytes.Equal(first.Body, second.Body) {
			t.Errorf("replayed body differs:\nfirst:  %s\nsecond: %s", first.Body, second.Body)
		}

		// One transition total: version 2, streak incremented once (planted 2).
		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("read ride: %v", err)
		}
		if persisted.Version != 2 || persisted.Streak != 3 {
			t.Errorf("version = %d, streak = %d; want 2 and 3", persisted.Version, persisted.Streak)
		}
	})

	t.Run("same key with a different request is ErrIdempotencyConflict", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)
		other := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(50), 1)
		ensureRound2(ctx, t)
		insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		token := rideToken("shared-key", "POST /v1/rides/:id/lock")
		if _, err := svc.RideLock(ctx, ride.ID, token, idempotentMarshal); err != nil {
			t.Fatalf("first RideLock() error = %v", err)
		}

		// Same key, different ride: the handler would hash the different URI,
		// so the stored hash no longer matches — the reuse must be refused.
		reused := rideToken("shared-key-other-ride", "POST /v1/rides/:id/lock")
		reused.Key = "shared-key"
		if _, err := svc.RideLock(ctx, other.ID, reused, idempotentMarshal); err != ErrIdempotencyConflict {
			t.Errorf("RideLock() error = %v, want ErrIdempotencyConflict", err)
		}

		persisted, err := svc.Store.Rides.Get(ctx, other.ID)
		if err != nil {
			t.Fatalf("read refused ride: %v", err)
		}
		if persisted.Version != 1 || persisted.State != RideWonPending {
			t.Errorf("refused ride version = %d, state = %s; want 1 and won_pending", persisted.Version, persisted.State)
		}
	})

	t.Run("concurrent duplicates execute the command exactly once", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideWonPending, decimal.NewFromInt(90), 2)
		ensureRound2(ctx, t)
		insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		token := rideToken("race-key", "POST /v1/rides/:id/lock")
		type call struct {
			res IdempotentResponse
			err error
		}
		results := make([]call, 2)

		var wg sync.WaitGroup
		wg.Add(2)
		for i := range results {
			go func(i int) {
				defer wg.Done()
				res, err := svc.RideLock(ctx, ride.ID, token, idempotentMarshal)
				results[i] = call{res, err}
			}(i)
		}
		wg.Wait()

		for i, r := range results {
			if r.err != nil {
				t.Fatalf("call %d error = %v", i, r.err)
			}
		}
		if !bytes.Equal(results[0].res.Body, results[1].res.Body) {
			t.Errorf("concurrent callers got different bodies:\n%s\n%s", results[0].res.Body, results[1].res.Body)
		}

		// Exactly one transition: the claim blocks the duplicate until the
		// winner commits, then the duplicate replays the stored response.
		persisted, err := svc.Store.Rides.Get(ctx, ride.ID)
		if err != nil {
			t.Fatalf("read ride: %v", err)
		}
		if persisted.Version != 2 || persisted.Streak != 3 {
			t.Errorf("version = %d, streak = %d; want 2 and 3", persisted.Version, persisted.Streak)
		}
	})

	t.Run("a failed command rolls its claim back", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		closedMatch := insertClosedMatch(ctx, t, 1, roundID, homeID, awayID, "2 hours")
		ride := plantRide(ctx, t, closedMatch, homeID, RideLost, decimal.Zero, 0)
		// A destination must exist so the failure comes from the state
		// machine (lost is terminal), not the schedule.
		ensureRound2(ctx, t)
		insertFutureMatch(ctx, t, 1, 2, homeID, awayID, "7 days")

		// Lost is terminal: the command fails after the claim, inside the tx.
		if _, err := svc.RideLock(ctx, ride.ID, rideToken("failed-key", "POST /v1/rides/:id/lock"), idempotentMarshal); err != ErrInvalidTransition {
			t.Errorf("RideLock() error = %v, want ErrInvalidTransition", err)
		}

		var keyRows int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, "failed-key").Scan(&keyRows); err != nil {
			t.Fatalf("count key rows: %v", err)
		}
		if keyRows != 0 {
			t.Errorf("failed-key rows = %d, want 0 (claim rolled back)", keyRows)
		}
	})
}
