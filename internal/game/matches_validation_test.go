package game

// Unit tests for ValidateMatch: pure logic, no database, microseconds.
// The `now` argument is injected, so every case controls the clock instead of
// racing against time.Now() — that is the whole point of the signature.

import (
	"testing"
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

func TestValidateMatch(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	i := func(n int) *int { return &n }

	valid := func() Match {
		return Match{
			SeasonID:   1,
			RoundID:    1,
			HomeTeamID: 1,
			AwayTeamID: 2,
			Status:     "open",
			StartsAt:   now.Add(24 * time.Hour),
			Odds:       Odds{Home: 1.5, Away: 2.5},
		}
	}

	tests := []struct {
		name     string
		mutate   func(m *Match)
		wantErrs map[string]string // field -> first error message; empty = valid
	}{
		{
			name:     "valid open match",
			mutate:   func(m *Match) {},
			wantErrs: map[string]string{},
		},
		{
			name: "valid closed match with score",
			mutate: func(m *Match) {
				m.Status = "closed"
				m.Score = Score{Home: i(88), Away: i(79)}
			},
			wantErrs: map[string]string{},
		},
		{
			name: "valid in_progress match at 0-0",
			mutate: func(m *Match) {
				m.Status = "in_progress"
				m.Score = Score{Home: i(0), Away: i(0)}
			},
			wantErrs: map[string]string{},
		},

		// Score pair rule: both nil or both set.
		{
			name: "home score without away score",
			mutate: func(m *Match) {
				m.Status = "closed"
				m.Score = Score{Home: i(88), Away: nil}
			},
			wantErrs: map[string]string{"score": "must contain both home and away values or neither"},
		},
		{
			name: "away score without home score",
			mutate: func(m *Match) {
				m.Status = "closed"
				m.Score = Score{Home: nil, Away: i(79)}
			},
			wantErrs: map[string]string{"score": "must contain both home and away values or neither"},
		},

		// Non-negative scores.
		{
			name: "negative home score",
			mutate: func(m *Match) {
				m.Status = "closed"
				m.Score = Score{Home: i(-1), Away: i(79)}
			},
			wantErrs: map[string]string{"score": "must not be negative"},
		},
		{
			name: "negative away score",
			mutate: func(m *Match) {
				m.Status = "closed"
				m.Score = Score{Home: i(88), Away: i(-1)}
			},
			wantErrs: map[string]string{"score": "must not be negative"},
		},

		// Status/score matrix: in_progress and closed require a score.
		{
			name: "in_progress without score",
			mutate: func(m *Match) {
				m.Status = "in_progress"
			},
			wantErrs: map[string]string{"score": "must be provided when the match is in progress or closed"},
		},
		{
			name: "closed without score",
			mutate: func(m *Match) {
				m.Status = "closed"
			},
			wantErrs: map[string]string{"score": "must be provided when the match is in progress or closed"},
		},

		// Status/score matrix: created, open and postponed forbid a score.
		{
			name: "created with score",
			mutate: func(m *Match) {
				m.Status = "created"
				m.Score = Score{Home: i(1), Away: i(0)}
			},
			wantErrs: map[string]string{"score": "must not be set before the match is in progress or closed"},
		},
		{
			name: "open with score",
			mutate: func(m *Match) {
				m.Score = Score{Home: i(1), Away: i(0)}
			},
			wantErrs: map[string]string{"score": "must not be set before the match is in progress or closed"},
		},
		{
			name: "postponed with score",
			mutate: func(m *Match) {
				m.Status = "postponed"
				m.Score = Score{Home: i(1), Away: i(0)}
			},
			wantErrs: map[string]string{"score": "must not be set before the match is in progress or closed"},
		},

		// starts_at: required and strictly after the injected now.
		{
			name: "starts_at missing",
			mutate: func(m *Match) {
				m.StartsAt = time.Time{}
			},
			wantErrs: map[string]string{"starts_at": "must be provided"},
		},
		{
			name: "starts_at in the past",
			mutate: func(m *Match) {
				m.StartsAt = now.Add(-1 * time.Second)
			},
			wantErrs: map[string]string{"starts_at": "must be in the future"},
		},
		{
			name: "starts_at exactly now",
			mutate: func(m *Match) {
				m.StartsAt = now
			},
			wantErrs: map[string]string{"starts_at": "must be in the future"},
		},
		{
			name: "starts_at just after now",
			mutate: func(m *Match) {
				m.StartsAt = now.Add(1 * time.Second)
			},
			wantErrs: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := valid()
			tt.mutate(&match)

			v := validator.New()
			ValidateMatch(v, match, now)

			if len(v.Errors) != len(tt.wantErrs) {
				t.Fatalf("got errors %v, want %v", v.Errors, tt.wantErrs)
			}
			for field, wantMsg := range tt.wantErrs {
				if gotMsg, ok := v.Errors[field]; !ok {
					t.Errorf("expected error on field %q, got errors %v", field, v.Errors)
				} else if gotMsg != wantMsg {
					t.Errorf("field %q: got message %q, want %q", field, gotMsg, wantMsg)
				}
			}
		})
	}
}
