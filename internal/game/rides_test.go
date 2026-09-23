package game

// Unit tests for the ride command methods. Each command is exercised along
// three axes: a valid transition (asserting state AND side effects), the
// phase guard, and the transition table. Terminal states are covered
// separately: from a terminal state every command must fail with
// ErrInvalidTransition, regardless of phase.
//
// The tests build Ride values as literals — Insert is a persistence stub
// and mutating the package-level rides slice from tests would couple tests
// to each other. WonPending and Lock read the package-level matches stub
// until service extraction, so the happy-path cases pin the stub values
// they depend on (all stub Home odds are 1.75; next match is ID 1,
// round 1).

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func lockedRide() *Ride {
	return &Ride{
		ID:           1,
		PlayerID:     10,
		TeamID:       20,
		MatchID:      1, // arbitrary: odds come from the matches stub, not this ID
		RoundID:      1,
		SeasonID:     1,
		State:        RideLocked,
		TokensLocked: decimal.NewFromInt(100),
		Acc:          decimal.Zero,
		Streak:       0,
	}
}

func wonPendingRide() *Ride {
	r := lockedRide()
	r.State = RideWonPending
	r.Streak = 1
	r.Acc = decimal.NewFromInt(90)

	return r
}

func TestRide_WonPending(t *testing.T) {
	t.Run("from locked during match phase wins and credits acc", func(t *testing.T) {
		r := lockedRide()

		err := r.WonPending(MatchPhase)

		if err != nil {
			t.Fatalf("WonPending() = %v, want nil", err)
		}
		if r.State != RideWonPending {
			t.Fatalf("state = %q, want %q", r.State, RideWonPending)
		}
		// Stub odds 1.75, tokens 100, streak 0: delta = 100 * 0.75 * 1 = 75.
		if !r.Acc.Equal(decimal.NewFromInt(75)) {
			t.Fatalf("acc = %s, want 75", r.Acc)
		}
	})

	t.Run("outside match phase is rejected and changes nothing", func(t *testing.T) {
		for _, phase := range []RoundPhase{ActionPhase, DecisionPhase} {
			r := lockedRide()

			err := r.WonPending(phase)

			if !errors.Is(err, ErrInvalidRoundPhase) {
				t.Fatalf("WonPending(%q) = %v, want ErrInvalidRoundPhase", phase, err)
			}
			if r.State != RideLocked {
				t.Fatalf("state = %q, want unchanged %q", r.State, RideLocked)
			}
		}
	})
}

func TestRide_Lost(t *testing.T) {
	t.Run("from locked during match phase loses and zeroes acc", func(t *testing.T) {
		r := lockedRide()
		r.Acc = decimal.NewFromInt(50)

		err := r.Lost(MatchPhase)

		if err != nil {
			t.Fatalf("Lost() = %v, want nil", err)
		}
		if r.State != RideLost {
			t.Fatalf("state = %q, want %q", r.State, RideLost)
		}
		if !r.Acc.IsZero() {
			t.Fatalf("acc = %s, want 0", r.Acc)
		}
	})

	t.Run("outside match phase is rejected", func(t *testing.T) {
		r := lockedRide()

		err := r.Lost(DecisionPhase)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("Lost() = %v, want ErrInvalidRoundPhase", err)
		}
		if r.State != RideLocked {
			t.Fatalf("state = %q, want unchanged %q", r.State, RideLocked)
		}
	})
}

func TestRide_Lock(t *testing.T) {
	t.Run("from won_pending during decision phase re-points to next match", func(t *testing.T) {
		r := wonPendingRide()
		before := r.Streak

		err := r.Lock(DecisionPhase)

		if err != nil {
			t.Fatalf("Lock() = %v, want nil", err)
		}
		if r.State != RideLocked {
			t.Fatalf("state = %q, want %q", r.State, RideLocked)
		}
		// Stub next match: ID 1, round 1.
		if r.MatchID != 1 || r.RoundID != 1 {
			t.Fatalf("match/round = %d/%d, want 1/1", r.MatchID, r.RoundID)
		}
		if r.Streak != before+1 {
			t.Fatalf("streak = %d, want %d", r.Streak, before+1)
		}
	})

	t.Run("outside decision phase is rejected", func(t *testing.T) {
		for _, phase := range []RoundPhase{ActionPhase, MatchPhase} {
			r := wonPendingRide()

			err := r.Lock(phase)

			if !errors.Is(err, ErrInvalidRoundPhase) {
				t.Fatalf("Lock(%q) = %v, want ErrInvalidRoundPhase", phase, err)
			}
			if r.State != RideWonPending {
				t.Fatalf("state = %q, want unchanged %q", r.State, RideWonPending)
			}
		}
	})
}

func TestRide_Burn(t *testing.T) {
	t.Run("from won_pending during decision phase burns and keeps acc", func(t *testing.T) {
		r := wonPendingRide()
		accBefore := r.Acc

		err := r.Burn(DecisionPhase)

		if err != nil {
			t.Fatalf("Burn() = %v, want nil", err)
		}
		if r.State != RideBurned {
			t.Fatalf("state = %q, want %q", r.State, RideBurned)
		}
		// Burn pays out acc; the payout itself is a service concern.
		if !r.Acc.Equal(accBefore) {
			t.Fatalf("acc = %s, want unchanged %s", r.Acc, accBefore)
		}
	})

	t.Run("outside decision phase is rejected", func(t *testing.T) {
		r := wonPendingRide()

		err := r.Burn(MatchPhase)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("Burn() = %v, want ErrInvalidRoundPhase", err)
		}
		if r.State != RideWonPending {
			t.Fatalf("state = %q, want unchanged %q", r.State, RideWonPending)
		}
	})
}

func TestRide_Unlock(t *testing.T) {
	t.Run("from won_pending during decision phase unlocks and forfeits acc", func(t *testing.T) {
		r := wonPendingRide()

		err := r.Unlock(DecisionPhase)

		if err != nil {
			t.Fatalf("Unlock() = %v, want nil", err)
		}
		if r.State != RideUnlocked {
			t.Fatalf("state = %q, want %q", r.State, RideUnlocked)
		}
		if !r.Acc.IsZero() {
			t.Fatalf("acc = %s, want 0", r.Acc)
		}
	})

	t.Run("outside decision phase is rejected", func(t *testing.T) {
		r := wonPendingRide()

		err := r.Unlock(ActionPhase)

		if !errors.Is(err, ErrInvalidRoundPhase) {
			t.Fatalf("Unlock() = %v, want ErrInvalidRoundPhase", err)
		}
		if r.State != RideWonPending {
			t.Fatalf("state = %q, want unchanged %q", r.State, RideWonPending)
		}
	})
}

// TestRide_TerminalStates pins the machine's exit guarantee: once a ride
// reaches burned, unlocked or lost, no command may move it again. The
// transition-table check fires before the phase check, so the phase
// argument is irrelevant here — passing the "correct" phase per command
// proves the table alone rejects terminal states.
func TestRide_TerminalStates(t *testing.T) {
	terminal := []RideState{RideBurned, RideUnlocked, RideLost}

	type command struct {
		name  string
		phase RoundPhase
		call  func(r *Ride) error
	}

	commands := []command{
		{"won_pending", MatchPhase, func(r *Ride) error { return r.WonPending(MatchPhase) }},
		{"lost", MatchPhase, func(r *Ride) error { return r.Lost(MatchPhase) }},
		{"lock", DecisionPhase, func(r *Ride) error { return r.Lock(DecisionPhase) }},
		{"burn", DecisionPhase, func(r *Ride) error { return r.Burn(DecisionPhase) }},
		{"unlock", DecisionPhase, func(r *Ride) error { return r.Unlock(DecisionPhase) }},
	}

	for _, from := range terminal {
		for _, cmd := range commands {
			t.Run(string(from)+" rejects "+cmd.name, func(t *testing.T) {
				r := lockedRide()
				r.State = from

				err := cmd.call(r)

				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("command %s from %s = %v, want ErrInvalidTransition", cmd.name, from, err)
				}
				if r.State != from {
					t.Fatalf("state = %q, want unchanged %q", r.State, from)
				}
			})
		}
	}
}

// TestRide_InvalidTransitions covers the non-terminal rejections the
// transition table must make: won_pending cannot win or lose again, locked
// cannot take a player decision.
func TestRide_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		ride *Ride
		call func(r *Ride) error
	}{
		{
			name: "won_pending cannot become won_pending",
			ride: wonPendingRide(),
			call: func(r *Ride) error { return r.WonPending(MatchPhase) },
		},
		{
			name: "won_pending cannot become lost",
			ride: wonPendingRide(),
			call: func(r *Ride) error { return r.Lost(MatchPhase) },
		},
		{
			name: "locked cannot lock",
			ride: lockedRide(),
			call: func(r *Ride) error { return r.Lock(DecisionPhase) },
		},
		{
			name: "locked cannot burn",
			ride: lockedRide(),
			call: func(r *Ride) error { return r.Burn(DecisionPhase) },
		},
		{
			name: "locked cannot unlock",
			ride: lockedRide(),
			call: func(r *Ride) error { return r.Unlock(DecisionPhase) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.ride.State

			err := tt.call(tt.ride)

			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("got %v, want ErrInvalidTransition", err)
			}
			if tt.ride.State != before {
				t.Fatalf("state = %q, want unchanged %q", tt.ride.State, before)
			}
		})
	}
}

// TestRide_Insert documents the persistence stub's current behavior so the
// switch to the store cannot silently change the initial state contract:
// a fresh ride is locked, with zero acc and streak.
func TestRide_Insert(t *testing.T) {
	ride, err := new(Ride).Insert(1, 2, 3, 4, 5, decimal.NewFromInt(100), decimal.NewFromInt(90))

	if err != nil {
		t.Fatalf("Insert() = %v, want nil", err)
	}
	if ride.State != RideLocked {
		t.Fatalf("state = %q, want %q", ride.State, RideLocked)
	}
	if !ride.Acc.IsZero() {
		t.Fatalf("acc = %s, want 0", ride.Acc)
	}
	if ride.Streak != 0 {
		t.Fatalf("streak = %d, want 0", ride.Streak)
	}
	if ride.CreatedAt.After(time.Now()) {
		t.Fatalf("created_at %v is in the future", ride.CreatedAt)
	}
}
