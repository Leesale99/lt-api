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
//  3. Check 5 of rides_block_illegal_transition
//     (migrations/000011_add_rides_result_agreement_gate.up.sql) — the SQL
//     enforcement home: the database is where the invariant lives, so no
//     writer (app, script, future refactor) can disagree with the scores.
//
// Both tests below read the same scoreStateRule vectors: the unit side
// asserts the Winner→state mapping, the DB side runs the settlement
// handler against a planted closed match (the payload derives, the gate
// agrees silently) and then probes the gate's refusals directly — the
// destination the fixture names must be admitted, its opposite refused. A
// rule change must fail exactly one site first; the change-detector
// comments at the three sites lead from any failure to the other two
// within a minute.

import (
	"context"
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
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
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
// names (from the payload, through the DB gate), and the gate must refuse
// the contradicting destination for each side (P0001 — the code the store
// maps to ErrInvalidTransition).
func TestScoreStateRuleDB(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	opposite := func(s RideState) RideState {
		if s == RideWonPending {
			return RideLost
		}
		return RideWonPending
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

			// Gate probes: fresh locked rides (the first two are out of
			// locked now), each driven to the destination contradicting the
			// fixture. Check 5 must refuse with P0001.
			probeHome := plantRide(ctx, t, matchID, homeID, RideLocked, decimal.Zero, 0)
			probeAway := plantRide(ctx, t, matchID, awayID, RideLocked, decimal.Zero, 0)

			_, err := pool.Exec(ctx, `UPDATE rides SET state = $2 WHERE id = $1`, probeHome.ID, opposite(tt.wantHome))
			assertCode(t, constraintCase{name: tt.name + " (home side, contradicting destination refused)", wantCode: "P0001"}, err)

			_, err = pool.Exec(ctx, `UPDATE rides SET state = $2 WHERE id = $1`, probeAway.ID, opposite(tt.wantAway))
			assertCode(t, constraintCase{name: tt.name + " (away side, contradicting destination refused)", wantCode: "P0001"}, err)
		})
	}
}
