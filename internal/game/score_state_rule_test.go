package game

// score_state_rule_test.go is the shared fixture tying the three encodings
// of the score→state rule together (ADR-018/ADR-020): a closed match's
// final scores decide every locked ride's destination, and a draw crowns
// no winner.
//
// The three encodings:
//
//  1. schedule.Match.Winner (internal/schedule/matches.go) — the publisher's
//     derivation, carried in the event payload.
//  2. The payload→destination derivation in game's settlement handler
//     (internal/game/events.go, settleRideFromResult) — the consumer's copy.
//  3. The resultAgreement check in that same handler (internal/game/
//     events.go) — the enforcement home since migration 000017 (the old
//     rides_state_gate check 5 read schedule's table from a game trigger,
//     the cross-context read Phase D removed).
//
// Both tests below read the same scoreStateRule vectors: the unit side
// asserts the Winner→state mapping, the DB side runs the settlement
// handler against a planted closed match and probes the enforcement — a
// lying payload (a destination the committed scores contradict) must be
// refused with ErrInvalidTransition and leave the rides untouched. A rule
// change must fail exactly one site first; the change-detector comments
// at the three sites lead from any failure to the other two within a
// minute.

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/schedule"
)

// scoreStateRule is the fixture: one vector per final-score pairing,
// carrying the expected destination for rides on either side. Six
// (home_score, away_score, ride team, expected state) tuples — the draw
// row is the highest-value one, because it is where the three encodings
// phrase the rule differently (Winner returns nil; the CASE falls through
// to ELSE 'lost'; check 5 compares won_pending against a false team_won).
var scoreStateRule = []struct {
	name               string
	home, away         int // closed-match final score
	wantHome, wantAway RideState
}{
	{"home side wins: home ride won_pending, away ride lost", 3, 1, RideWonPending, RideLost},
	{"away side wins: away ride won_pending, home ride lost", 1, 3, RideLost, RideWonPending},
	{"draw crowns no winner: both rides lost", 2, 2, RideLost, RideLost},
}

// insertClosedMatchScored plants a closed match carrying the given final
// scores (insertClosedMatch hardcodes 88/79; the fixture needs arbitrary
// vectors). started 3 hours before ending, same shape as the shared one.
func insertClosedMatchScored(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID, home, away int) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, $5, $6, 'closed', now() - interval '5 hours', now() - interval '2 hours')
		RETURNING id
	`, seasonID, roundID, homeID, awayID, home, away).Scan(&id)
	if err != nil {
		t.Fatalf("insert scored closed match: %v", err)
	}

	return id
}

// TestWinnerScoreStateRule asserts the Go derivation against the shared
// fixture: expected state is won_pending iff Winner() crowns the ride's
// team. That mapping is itself part of the rule statement, so it is
// derived here rather than duplicated as a second expectation column.
func TestWinnerScoreStateRule(t *testing.T) {
	i := func(n int) *int { return &n }

	for _, tt := range scoreStateRule {
		t.Run(tt.name, func(t *testing.T) {
			m := schedule.Match{
				Status:     schedule.MatchClosed,
				HomeTeamID: 1,
				AwayTeamID: 2,
				Score:      schedule.Score{Home: i(tt.home), Away: i(tt.away)},
			}

			winner := m.Winner()
			derived := func(teamID int) RideState {
				if winner != nil && *winner == teamID {
					return RideWonPending
				}
				return RideLost
			}

			if got := derived(m.HomeTeamID); got != tt.wantHome {
				t.Errorf("home side: Winner→%s, want %s", got, tt.wantHome)
			}
			if got := derived(m.AwayTeamID); got != tt.wantAway {
				t.Errorf("away side: Winner→%s, want %s", got, tt.wantAway)
			}
		})
	}
}

// TestScoreStateRuleDB runs the fixture against the database: per vector,
// the settlement handler must land both rides on the states the fixture
// names, and the enforcement (the service's resultAgreement check —
// migration 000017 moved it out of the trigger) must refuse a lying
// payload: a destination the committed scores contradict is refused with
// ErrInvalidTransition (the same error the store mapped the old trigger's
// P0001 refusals to) and the rides stay untouched.
func TestScoreStateRuleDB(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	// lyingWinner inverts the payload's outcome for one side: when the
	// fixture expects the side's ride to WIN (want won_pending, the
	// committed scores crown the team), the lying payload claims the OTHER
	// side won — derivation lands lost; when the fixture expects a loss,
	// the lying payload crowns the ride's own team — derivation lands
	// won_pending. Either way the derived destination contradicts the
	// committed result and resultAgreement must refuse.
	lyingWinner := func(want RideState, teamID, otherID int) *int {
		if want == RideWonPending {
			return &otherID
		}
		return &teamID
	}

	for _, tt := range scoreStateRule {
		t.Run(tt.name, func(t *testing.T) {
			cleanup(ctx, t)
			seasonID, roundID, homeID, awayID := seed(ctx, t)

			matchID := insertClosedMatchScored(ctx, t, seasonID, roundID, homeID, awayID, tt.home, tt.away)
			homeRide := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)
			awayRide := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

			// The settlement, driven by the payload the publisher would
			// send (the winner derived the way schedule.Match.Winner does).
			event := schedule.MatchResultRecorded{
				EventID:      "evt-" + tt.name,
				MatchID:      matchID,
				RoundID:      roundID,
				SeasonID:     seasonID,
				HomeTeamID:   homeID,
				AwayTeamID:   awayID,
				Score:        resolveScore(tt.home, tt.away),
				Odds:         schedule.Odds{Home: 1.75, Away: 2.20},
				WinnerTeamID: winnerOf(tt.home, tt.away, homeID, awayID),
			}
			svc, _ := newTestService()
			if err := svc.OnMatchResultRecorded(ctx, event); err != nil {
				t.Fatalf("OnMatchResultRecorded: %v", err)
			}

			states := make(map[int]RideState, 2)
			for _, ride := range []Ride{homeRide, awayRide} {
				got, err := svc.Store.Rides.Get(ctx, ride.ID)
				if err != nil {
					t.Fatalf("Get(%d): %v", ride.ID, err)
				}
				states[got.ID] = got.State
				if got.Version != 2 {
					t.Errorf("ride %d version = %d, want 2 (one transition)", got.ID, got.Version)
				}
			}
			if states[homeRide.ID] != tt.wantHome {
				t.Errorf("home ride: landed %s, want %s", states[homeRide.ID], tt.wantHome)
			}
			if states[awayRide.ID] != tt.wantAway {
				t.Errorf("away ride: landed %s, want %s", states[awayRide.ID], tt.wantAway)
			}

			// Enforcement probes: fresh locked rides (the first two are out
			// of locked now), settled with a LYING payload whose destination
			// contradicts the committed scores. resultAgreement must refuse
			// with ErrInvalidTransition and leave the rides locked/v1 (the
			// whole settlement tx rolled back).
			probeHome := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)
			probeAway := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

			lyingHome := event
			lyingHome.EventID = tt.name + " (home side, lying payload refused)"
			lyingHome.WinnerTeamID = lyingWinner(tt.wantHome, homeID, awayID)
			err := svc.OnMatchResultRecorded(ctx, lyingHome)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("lying home payload: OnMatchResultRecorded = %v, want ErrInvalidTransition", err)
			}

			lyingAway := event
			lyingAway.EventID = tt.name + " (away side, lying payload refused)"
			lyingAway.WinnerTeamID = lyingWinner(tt.wantAway, awayID, homeID)
			err = svc.OnMatchResultRecorded(ctx, lyingAway)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("lying away payload: OnMatchResultRecorded = %v, want ErrInvalidTransition", err)
			}

			for _, ride := range []Ride{probeHome, probeAway} {
				got, err := svc.Store.Rides.Get(ctx, ride.ID)
				if err != nil {
					t.Fatalf("Get(%d): %v", ride.ID, err)
				}
				if got.State != RideLocked || got.Version != 1 {
					t.Errorf("ride %d = %s/v%d, want untouched locked/v1 (the tx rolled back)", ride.ID, got.State, got.Version)
				}
			}
		})
	}
}
