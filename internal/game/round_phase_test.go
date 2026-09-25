package game

// Unit tests for Phase. The boundary convention — an instant landing exactly
// on a phase edge belongs to the LATER phase — is a contract, not an
// implementation detail: these tests are what locks it in, so a refactor of
// the derivation cannot silently flip edge ownership.

import (
	"testing"
	"time"
)

func TestPhase(t *testing.T) {
	first := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	last := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		now          time.Time
		firstMatchAt time.Time
		lastEndsAt   *time.Time
		want         RoundPhase
	}{
		{
			name:         "well before the lead time is action",
			now:          first.Add(-48 * time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         ActionPhase,
		},
		{
			name:         "one nanosecond before the lead time is still action",
			now:          first.Add(-time.Hour - time.Nanosecond),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         ActionPhase,
		},
		{
			// Boundary instant: belongs to the later phase.
			name:         "exactly at the lead time is match",
			now:          first.Add(-time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         MatchPhase,
		},
		{
			name:         "between first and last is match",
			now:          first.Add(3 * time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         MatchPhase,
		},
		{
			// No match has ended yet: the decision window cannot have opened.
			// Locks the nil-guard semantics (nil is MatchPhase, not Decision).
			name:         "no match ended yet is match",
			now:          last.Add(48 * time.Hour),
			firstMatchAt: first,
			lastEndsAt:   nil,
			want:         MatchPhase,
		},
		{
			name:         "one nanosecond before the lag end is still match",
			now:          last.Add(time.Hour - time.Nanosecond),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         MatchPhase,
		},
		{
			// Boundary instant: belongs to the later phase.
			name:         "exactly at the lag end is decision",
			now:          last.Add(time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         DecisionPhase,
		},
		{
			name:         "well after the lag end is decision",
			now:          last.Add(24 * time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &last,
			want:         DecisionPhase,
		},
		{
			// Unscheduled round: no match times, cannot derive boundaries.
			name:         "zero first match time is action",
			now:          time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
			firstMatchAt: time.Time{},
			lastEndsAt:   nil,
			want:         ActionPhase,
		},
		{
			// Single-match round: first start and last end are the same instant.
			name:         "single-match round is match inside its window",
			now:          first.Add(30 * time.Minute),
			firstMatchAt: first,
			lastEndsAt:   &first,
			want:         MatchPhase,
		},
		{
			name:         "single-match round is decision after its window",
			now:          first.Add(2 * time.Hour),
			firstMatchAt: first,
			lastEndsAt:   &first,
			want:         DecisionPhase,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Phase(tt.now, tt.firstMatchAt, tt.lastEndsAt)

			if got != tt.want {
				t.Fatalf("Phase(%v, %v, %v) = %q, want %q", tt.now, tt.firstMatchAt, tt.lastEndsAt, got, tt.want)
			}
		})
	}
}
