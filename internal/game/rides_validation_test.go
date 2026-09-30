package game

// Unit tests for the ride validators: pure logic, no database, microseconds.
// They pin the request-shape contract the handler checks before any store
// call — the DB CHECKs (constraints_test.go) and the domain commands
// (rides_test.go) enforce the same rules one layer down, these tests keep
// the validator from drifting from both.

import (
	"testing"

	"github.com/shopspring/decimal"
	"lt-api.aleksrdvn.com/internal/validator"
)

func TestValidateRide(t *testing.T) {
	valid := func() Ride {
		return Ride{
			PlayerID:     1,
			TeamID:       2,
			MatchID:      3,
			TokensLocked: decimal.NewFromInt(100),
		}
	}

	tests := []struct {
		name     string
		mutate   func(r *Ride)
		wantErrs map[string]string // field -> first error message; empty = valid
	}{
		{
			name:     "valid ride",
			mutate:   func(r *Ride) {},
			wantErrs: map[string]string{},
		},
		{
			name:     "zero player_id is rejected",
			mutate:   func(r *Ride) { r.PlayerID = 0 },
			wantErrs: map[string]string{"player_id": "must be provided"},
		},
		{
			name:     "zero team_id is rejected",
			mutate:   func(r *Ride) { r.TeamID = 0 },
			wantErrs: map[string]string{"team_id": "must be provided"},
		},
		{
			name:     "zero match_id is rejected",
			mutate:   func(r *Ride) { r.MatchID = 0 },
			wantErrs: map[string]string{"match_id": "must be provided"},
		},
		{
			name:     "zero tokens_locked is rejected",
			mutate:   func(r *Ride) { r.TokensLocked = decimal.Zero },
			wantErrs: map[string]string{"tokens_locked": "must be greater than zero"},
		},
		{
			name:     "negative tokens_locked is rejected",
			mutate:   func(r *Ride) { r.TokensLocked = decimal.NewFromInt(-1) },
			wantErrs: map[string]string{"tokens_locked": "must be greater than zero"},
		},
		{
			name: "all errors reported at once",
			mutate: func(r *Ride) {
				r.PlayerID = 0
				r.TeamID = 0
				r.MatchID = 0
				r.TokensLocked = decimal.Zero
			},
			wantErrs: map[string]string{
				"player_id":     "must be provided",
				"team_id":       "must be provided",
				"match_id":      "must be provided",
				"tokens_locked": "must be greater than zero",
			},
		},
		// Domain-owned fields are deliberately NOT validated here: state,
		// acc and streak are overwritten by ride.Create (ADR-019), so their
		// request values are garbage-in-garbage-out — Create discards them.
		{
			name: "domain-owned garbage does not affect validation",
			mutate: func(r *Ride) {
				r.State = RideBurned
				r.Acc = decimal.NewFromInt(-5)
				r.Streak = -1
			},
			wantErrs: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ride := valid()
			tt.mutate(&ride)

			v := validator.New()
			ValidateRide(v, ride)

			if tt.wantErrs == nil || len(tt.wantErrs) == 0 {
				if !v.Valid() {
					t.Fatalf("ValidateRide() = %v, want no errors", v.Errors)
				}
				return
			}

			if v.Valid() {
				t.Fatal("ValidateRide() = valid, want errors")
			}
			for field, want := range tt.wantErrs {
				if got, ok := v.Errors[field]; !ok || got != want {
					t.Errorf("errors[%q] = %q (present: %v), want %q", field, got, ok, want)
				}
			}
		})
	}
}

func TestValidateRideState(t *testing.T) {
	// Every state in the Go vocabulary must pass: this is the drift guard
	// against the DB CHECK (rides_state_check) — a state added to one side
	// only fails exactly one of these suites.
	for _, state := range rideStates {
		t.Run("accepts "+string(state), func(t *testing.T) {
			v := validator.New()
			ValidateRideState(v, state)

			if !v.Valid() {
				t.Fatalf("ValidateRideState(%q) = %v, want no errors", state, v.Errors)
			}
		})
	}

	rejected := []RideState{"", "won", "LOCKED", "burned ", "lost "}
	for _, state := range rejected {
		t.Run("rejects "+string(state), func(t *testing.T) {
			v := validator.New()
			ValidateRideState(v, state)

			if v.Valid() {
				t.Fatalf("ValidateRideState(%q) = valid, want error", state)
			}
			if v.Errors["state"] != "must be one of: locked, won_pending, burned, unlocked, lost" {
				t.Fatalf("errors[state] = %q, want the vocabulary message", v.Errors["state"])
			}
		})
	}
}
