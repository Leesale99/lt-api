package game

// Unit tests for calculateBonus. The formula under test:
//
//	delta = tokensLocked * (odds - 1) * (1 + 0.20 * streak)
//
// A streak of 0 credits the round's net winnings only; each continued
// streak level adds 20% of the net winnings. acc accumulates across rounds.

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCalculateBonus(t *testing.T) {
	d := decimal.NewFromInt

	tests := []struct {
		name         string
		matchOdds    decimal.Decimal
		tokensLocked decimal.Decimal
		acc          decimal.Decimal
		streak       int
		want         decimal.Decimal
	}{
		{
			name:         "first win credits net winnings only",
			matchOdds:    d(175).Div(d(100)),
			tokensLocked: d(100),
			acc:          d(0),
			streak:       0,
			want:         d(75),
		},
		{
			name:         "streak one adds twenty percent",
			matchOdds:    d(175).Div(d(100)),
			tokensLocked: d(100),
			acc:          d(0),
			streak:       1,
			want:         d(90),
		},
		{
			name:         "streak accumulates on existing acc",
			matchOdds:    d(175).Div(d(100)),
			tokensLocked: d(100),
			acc:          d(75),
			streak:       2,
			// delta = 100 * 0.75 * 1.4 = 105; 75 + 105 = 180
			want: d(180),
		},
		{
			name:         "higher odds scale the winnings",
			matchOdds:    d(3),
			tokensLocked: d(50),
			acc:          d(0),
			streak:       0,
			want:         d(100),
		},
		{
			name:         "long streak multiplies the payout",
			matchOdds:    d(2),
			tokensLocked: d(10),
			acc:          d(0),
			streak:       5,
			// delta = 10 * 1 * 2 = 20 (streak 5 doubles the net winnings)
			want: d(20),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateBonus(tt.matchOdds, tt.tokensLocked, tt.acc, tt.streak)

			if !got.Equal(tt.want) {
				t.Fatalf("calculateBonus(%s, %s, %s, %d) = %s, want %s",
					tt.matchOdds, tt.tokensLocked, tt.acc, tt.streak, got, tt.want)
			}
		})
	}
}
