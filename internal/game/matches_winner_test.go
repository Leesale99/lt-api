package game

// Unit tests for Winner. Winner relies on a domain invariant rather than a
// nil guard: a closed match always carries a complete score, enforced
// upstream by ValidateMatch and by the matches_status_score_check /
// matches_score_complete_check DB constraints. "Closed without score" is
// therefore not a representable state and deliberately has no test case —
// testing it would test an impossible input, not logic.

import "testing"

func TestWinner(t *testing.T) {
	i := func(n int) *int { return &n }

	closed := func(home, away, homeTeamID, awayTeamID int) Match {
		return Match{
			Status:     "closed",
			HomeTeamID: homeTeamID,
			AwayTeamID: awayTeamID,
			Score:      Score{Home: i(home), Away: i(away)},
		}
	}

	tests := []struct {
		name  string
		match Match
		want  *int
	}{
		{
			name:  "home team wins",
			match: closed(88, 79, 1, 2),
			want:  i(1),
		},
		{
			name:  "away team wins",
			match: closed(79, 88, 1, 2),
			want:  i(2),
		},
		{
			name:  "draw returns nil",
			match: closed(80, 80, 1, 2),
			want:  nil,
		},
		{
			name: "created match without a score returns nil",
			match: Match{
				Status:     "created",
				HomeTeamID: 1,
				AwayTeamID: 2,
			},
			want: nil,
		},
		{
			name:  "created match without a score returns nil",
			match: Match{Status: "created", HomeTeamID: 1, AwayTeamID: 2},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.match.Winner()

			switch {
			case got == nil && tt.want == nil:
				// ok
			case got == nil || tt.want == nil:
				t.Fatalf("Winner() = %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Fatalf("Winner() = %d, want %d", *got, *tt.want)
			}
		})
	}
}
