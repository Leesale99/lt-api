package game

// Unit tests for the ride command methods. Each command is exercised along
// three axes: a valid transition (asserting state AND side effects), the
// phase guard, and the transition table. Terminal states are covered
// separately: from a terminal state every command must fail with
// ErrInvalidTransition, regardless of phase.
//
// The tests build Ride values as literals — Insert is a persistence stub
// (tests plant rides through it and reset the slice via resetRides) and
// mutating it from tests would couple tests to each other. Command inputs
// (odds, next-match ID) come from the stores today; the tests pass the
// values they were written against (odds 1.75, next match ID 1) as
// literals.

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

// stubOdds is the odds the command tests pass through to the domain. The
// bonus arithmetic is pinned exhaustively in ride_bonus_test.go; the
// command tests assert only the wiring — that WonPending delegates to
// calculateBonus with the odds it was given — not the formula itself.
var stubOdds = decimal.NewFromFloat(1.75)

func lockedRide() *Ride {
	return &Ride{
		ID:           1,
		PlayerID:     10,
		TeamID:       20,
		MatchID:      1, // arbitrary: odds are passed literally, not looked up
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
	t.Run("from locked during match phase wins and credits the bonus delta", func(t *testing.T) {
		r := lockedRide()
		r.Acc = decimal.NewFromInt(40)
		r.Streak = 2

		err := r.WonPending(MatchPhase, stubOdds)

		if err != nil {
			t.Fatalf("WonPending() = %v, want nil", err)
		}
		if r.State != RideWonPending {
			t.Fatalf("state = %q, want %q", r.State, RideWonPending)
		}
		// One decision per test: this pin is the wiring — the command passes
		// the incoming odds through to calculateBonus on top of the existing
		// acc — while the formula itself is pinned in ride_bonus_test.go.
		want := calculateBonus(stubOdds, r.TokensLocked, decimal.NewFromInt(40), 2)
		if !r.Acc.Equal(want) {
			t.Fatalf("acc = %s, want %s (calculateBonus wiring)", r.Acc, want)
		}
	})

	t.Run("outside match phase is rejected and changes nothing", func(t *testing.T) {
		for _, phase := range []RoundPhase{ActionPhase, DecisionPhase} {
			r := lockedRide()

			err := r.WonPending(phase, stubOdds)

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

		err := r.Lock(DecisionPhase, 1)

		if err != nil {
			t.Fatalf("Lock() = %v, want nil", err)
		}
		if r.State != RideLocked {
			t.Fatalf("state = %q, want %q", r.State, RideLocked)
		}
		// Stub next match: ID 1.
		if r.MatchID != 1 {
			t.Fatalf("match = %d, want 1", r.MatchID)
		}
		if r.Streak != before+1 {
			t.Fatalf("streak = %d, want %d", r.Streak, before+1)
		}
	})

	t.Run("outside decision phase is rejected", func(t *testing.T) {
		for _, phase := range []RoundPhase{ActionPhase, MatchPhase} {
			r := wonPendingRide()

			err := r.Lock(phase, 1)

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
// reaches burned, unlocked or lost, no command may move it again. Each
// command checks phase before the transition table, so each case passes
// the "correct" phase per command to prove the table alone rejects
// terminal states.
func TestRide_TerminalStates(t *testing.T) {
	terminal := []RideState{RideBurned, RideUnlocked, RideLost}

	type command struct {
		name  string
		phase RoundPhase
		call  func(r *Ride) error
	}

	commands := []command{
		{"won_pending", MatchPhase, func(r *Ride) error { return r.WonPending(MatchPhase, stubOdds) }},
		{"lost", MatchPhase, func(r *Ride) error { return r.Lost(MatchPhase) }},
		{"lock", DecisionPhase, func(r *Ride) error { return r.Lock(DecisionPhase, 1) }},
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
			call: func(r *Ride) error { return r.WonPending(MatchPhase, stubOdds) },
		},
		{
			name: "won_pending cannot become lost",
			ride: wonPendingRide(),
			call: func(r *Ride) error { return r.Lost(MatchPhase) },
		},
		{
			name: "locked cannot lock",
			ride: lockedRide(),
			call: func(r *Ride) error { return r.Lock(DecisionPhase, 1) },
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

// TestRide_Create pins the creation command: the ADR-019 initial-state
// contract (locked, zero acc and streak) is enforced by the domain, not by
// the request or the store — caller-supplied values for domain-owned
// fields are overwritten, so an invalid initial state cannot be produced
// through normal domain operations.
func TestRide_Create(t *testing.T) {
	t.Run("during action phase confirms the initial-state contract", func(t *testing.T) {
		r := Ride{
			PlayerID:     10,
			TeamID:       20,
			MatchID:      1,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
			// Caller-supplied domain-owned values must be discarded.
			State:  RideWonPending,
			Acc:    decimal.NewFromInt(5),
			Streak: 7,
		}

		err := r.Create(ActionPhase)

		if err != nil {
			t.Fatalf("Create() = %v, want nil", err)
		}
		if r.State != RideLocked {
			t.Fatalf("state = %q, want %q", r.State, RideLocked)
		}
		if !r.Acc.IsZero() {
			t.Fatalf("acc = %s, want 0", r.Acc)
		}
		if r.Streak != 0 {
			t.Fatalf("streak = %d, want 0", r.Streak)
		}
		// Inputs the caller owns are preserved.
		if r.TokensLocked.IsZero() || r.BaseAtLock.IsZero() {
			t.Fatal("caller-supplied tokens/base were lost")
		}
	})

	t.Run("outside action phase is rejected and changes nothing", func(t *testing.T) {
		for _, phase := range []RoundPhase{MatchPhase, DecisionPhase} {
			r := Ride{State: RideLocked, Acc: decimal.NewFromInt(5), Streak: 7}

			err := r.Create(phase)

			if !errors.Is(err, ErrInvalidRoundPhase) {
				t.Fatalf("Create(%q) = %v, want ErrInvalidRoundPhase", phase, err)
			}
			if r.State != RideLocked {
				t.Fatalf("state = %q, want unchanged %q", r.State, RideLocked)
			}
		}
	})
}
