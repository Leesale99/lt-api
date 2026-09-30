package api

// Ride handler tests: the HTTP contract of the ride endpoints against the
// real rides table (migration 000006) — request validation, error mapping
// (the gameErrorResponse switch: 404 / 409 round-not-open / 409 invalid
// phase / 409 invalid state) and the happy-path command flows.
//
// The canonical fixture gives rides exactly what they need:
//   - round 3 is open with matches 5/6/7 starting days out → ActionPhase,
//     the only phase ride creation accepts
//   - decision phase is manufactured inline (a closed match inside the open
//     round 3, ended 2 hours ago — the same clock-by-data trick the game
//     package tests use), because no fixture round is open AND decided
//   - player 1 (Sasha Vezenkov) exists; extra players are planted inline
//
// Rides are planted by direct INSERT — no producer for won_pending rides
// exists until Phase 04 (match resolution); the plant helpers below stand
// in for it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/store"
)

// plantPlayerRow inserts a player row and returns its id. Player 1 exists
// in the fixture; the pagination cases need a second one.
func plantPlayerRow(t *testing.T, teamID int) int {
	t.Helper()

	var id int
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO players (season_id, favorite_team_id, name)
		VALUES (2, $1, 'ride player')
		RETURNING id
	`, teamID).Scan(&id)
	if err != nil {
		t.Fatalf("plant player: %v", err)
	}

	return id
}

// plantRideRow inserts a ride with caller-chosen state and returns its id.
// Direct INSERT bypasses the state gate (BEFORE UPDATE only) — that is the
// point: tests need rides in states no current producer can reach.
func plantRideRow(t *testing.T, playerID, teamID, matchID int, state, acc string, streak int) int {
	t.Helper()

	var id int
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, $4, 100, 95, $5, $6)
		RETURNING id
	`, playerID, teamID, matchID, state, acc, streak).Scan(&id)
	if err != nil {
		t.Fatalf("plant ride: %v", err)
	}

	return id
}

// plantDecidedMatch inserts a closed match into the open round 3, ended
// `ago` before now, and returns its id. Round 3 then derives DecisionPhase
// (first start in the past, last end 2 hours back), while staying open —
// the state the decision commands (lock/burn/unlock) require.
func plantDecidedMatch(t *testing.T) int {
	t.Helper()

	var id int
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
		VALUES (1, 3, 1, 2, 1.75, 2.20, 88, 79, 'closed', now() - interval '3 hours', now() - $1::interval)
		RETURNING id
	`, "2 hours").Scan(&id)
	if err != nil {
		t.Fatalf("plant decided match: %v", err)
	}

	return id
}

func TestCreateRideHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid ride",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":5,"tokens_locked":100}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"player_id": 1`, `"team_id": 1`, `"match_id": 5`, `"state": "locked"`, `"tokens_locked": "100"`, `"version": 1`},
		},
		{
			// The handler's token lookup is a stub (TODO in rides.go): the
			// base is hard-wired to 1.95 until the ledger lands.
			name:     "base_at_lock comes from the token, not the request",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":5,"tokens_locked":100}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"base_at_lock": "1.95"`},
		},
		{
			name:     "missing fields are rejected",
			url:      "/v1/rides",
			body:     `{}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"player_id", "must be provided"},
		},
		{
			name:     "zero tokens_locked is rejected",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":5,"tokens_locked":0}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"tokens_locked", "must be greater than zero"},
		},
		{
			name:     "unknown key in body is rejected",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":5,"tokens_locked":100,"state":"burned"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "malformed body is rejected",
			url:      "/v1/rides",
			body:     `{"player_id":1,`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body contains badly-formed JSON"},
		},
		{
			name:     "unknown match is refused",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":999,"tokens_locked":100}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			// Match 1 sits in the closed round 1: RidePhase refuses before
			// any write, so nothing is created even on the happy-looking body.
			name:     "closed round is refused",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":1,"tokens_locked":100}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"ride commands require an open round"},
		},
		{
			// Match 8 sits in round 4, which is only 'created' — the ride's
			// round must be open, not merely scheduled.
			name:     "created (not open) round is refused",
			url:      "/v1/rides",
			body:     `{"player_id":1,"team_id":1,"match_id":8,"tokens_locked":100}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"ride commands require an open round"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			req := httptest.NewRequest(http.MethodPost, tt.url, strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

			if rr.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
			}
			for _, fragment := range tt.wantBody {
				if !strings.Contains(rr.Body.String(), fragment) {
					t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
				}
			}
		})
	}
}

func TestCreateRideHandlerLocation(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/rides", strings.NewReader(`{"player_id":1,"team_id":1,"match_id":5,"tokens_locked":100}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/v1/rides/1" {
		t.Errorf("Location = %q, want %q", got, "/v1/rides/1")
	}
}

func TestShowRideHandler(t *testing.T) {
	requireDB(t)

	t.Run("planted ride is served", func(t *testing.T) {
		reset(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, 5, "locked", "0", 0)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides/"+strconv.Itoa(rideID), nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		for _, fragment := range []string{`"player_id": ` + strconv.Itoa(playerID), `"match_id": 5`, `"state": "locked"`, `"tokens_locked": "100"`} {
			if !strings.Contains(rr.Body.String(), fragment) {
				t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
			}
		}
	})

	t.Run("ride not found", func(t *testing.T) {
		reset(t)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides/999", nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusNotFound, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "could not be found") {
			t.Errorf("body missing not-found message (body: %s)", rr.Body.String())
		}
	})
}

func TestListRidesHandler(t *testing.T) {
	requireDB(t)

	// Three rides across three players (fixture player 1 plus two planted),
	// two teams, two matches, two states: every filter gets a matching and
	// a non-matching case. Player IDs are returned — they are sequence-
	// assigned, not fixture-fixed.
	seed := func(t *testing.T) (player2, player3, r1, r2, r3 int) {
		t.Helper()
		reset(t)

		player2 = plantPlayerRow(t, 2)
		player3 = plantPlayerRow(t, 1)

		r1 = plantRideRow(t, 1, 1, 5, "locked", "0", 0) // fixture player 1, team 1
		r2 = plantRideRow(t, player2, 1, 6, "won_pending", "90", 1)
		r3 = plantRideRow(t, player3, 2, 5, "locked", "0", 0)

		return player2, player3, r1, r2, r3
	}

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "unfiltered list",
			url:      "/v1/rides",
			wantCode: http.StatusOK,
			wantBody: []string{`"state": "locked"`, `"state": "won_pending"`, `"total_records": 3`, `"last_page": 1`},
		},
		{
			name:     "player filter matching nothing",
			url:      "/v1/rides?player_id=999",
			wantCode: http.StatusOK,
			wantBody: []string{`"rides": []`, `"metadata": {}`},
		},
		{
			name:     "filter by team 1",
			url:      "/v1/rides?team_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			name:     "team filter matching nothing",
			url:      "/v1/rides?team_id=999",
			wantCode: http.StatusOK,
			wantBody: []string{`"rides": []`, `"metadata": {}`},
		},
		{
			name:     "filter by match 5",
			url:      "/v1/rides?match_id=5",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			name:     "match filter matching nothing",
			url:      "/v1/rides?match_id=999",
			wantCode: http.StatusOK,
			wantBody: []string{`"rides": []`, `"metadata": {}`},
		},
		{
			name:     "filter by state locked",
			url:      "/v1/rides?state=locked",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			// player_id=999 owns no rides: combined filters intersect.
			name:     "combined filters intersect",
			url:      "/v1/rides?player_id=999&state=locked",
			wantCode: http.StatusOK,
			wantBody: []string{`"rides": []`, `"metadata": {}`},
		},
		{
			name:     "unknown state value is rejected",
			url:      "/v1/rides?state=bogus",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"state", "must be one of"},
		},
		{
			name:     "page_size over the maximum",
			url:      "/v1/rides?page_size=101",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page_size", "must be a maximum of 100"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed(t)
			app := newTestApplication()

			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

			if rr.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, tt.wantCode, rr.Body.String())
			}
			for _, fragment := range tt.wantBody {
				if !strings.Contains(rr.Body.String(), fragment) {
					t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
				}
			}
		})
	}

	// Player-dependent filter cases: planted player IDs are not
	// fixture-fixed, so their URLs are built after seeding.
	t.Run("filter by the fixture player", func(t *testing.T) {
		seed(t)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides?player_id=1", nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"total_records": 1`) {
			t.Errorf("body missing total 1 (body: %s)", rr.Body.String())
		}
	})

	t.Run("filter by a planted player", func(t *testing.T) {
		player2, _, _, _, _ := seed(t)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides?player_id="+strconv.Itoa(player2), nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"total_records": 1`) {
			t.Errorf("body missing total 1 (body: %s)", rr.Body.String())
		}
	})

	t.Run("combined player and state filters intersect", func(t *testing.T) {
		_, player3, _, _, _ := seed(t)

		for url, wantTotal := range map[string]string{
			"/v1/rides?player_id=" + strconv.Itoa(player3) + "&state=locked":      `"total_records": 1`,
			"/v1/rides?player_id=" + strconv.Itoa(player3) + "&state=won_pending": `"metadata": {}`,
		} {
			app := newTestApplication()
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

			if rr.Code != http.StatusOK {
				t.Fatalf("%s: got status %d, want %d (body: %s)", url, rr.Code, http.StatusOK, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), wantTotal) {
				t.Errorf("%s: body missing %q (body: %s)", url, wantTotal, rr.Body.String())
			}
		}
	})

	t.Run("sort descending puts the newest ride first", func(t *testing.T) {
		_, _, r1, r2, r3 := seed(t)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides?sort=-id", nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		// Decode instead of substring-matching: `"id": 3` also occurs inside
		// `"player_id": 3`, so string positions are not order evidence.
		got := decodeRideList(t, rr)
		want := []int{r3, r2, r1}
		if len(got.Rides) != len(want) {
			t.Fatalf("got %d rides, want %d (body: %s)", len(got.Rides), len(want), rr.Body.String())
		}
		for i, w := range want {
			if got.Rides[i].ID != w {
				t.Errorf("rides[%d].id = %d, want %d (descending order)", i, got.Rides[i].ID, w)
			}
		}
	})

	t.Run("second page after page_size 2", func(t *testing.T) {
		_, _, r1, _, _ := seed(t)
		app := newTestApplication()

		req := httptest.NewRequest(http.MethodGet, "/v1/rides?sort=-id&page_size=2&page=2", nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

		if rr.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		got := decodeRideList(t, rr)
		if len(got.Rides) != 1 || got.Rides[0].ID != r1 {
			t.Fatalf("second page = %v, want only the oldest ride %d", got.Rides, r1)
		}
		if got.Metadata.CurrentPage != 2 || got.Metadata.LastPage != 2 || got.Metadata.TotalRecords != 3 {
			t.Errorf("metadata = %+v, want current 2 / last 2 / total 3", got.Metadata)
		}
	})
}

// rideListResponse decodes just enough of the list envelope to assert
// identity and pagination without string-position heuristics.
type rideListResponse struct {
	Rides []struct {
		ID int `json:"id"`
	} `json:"rides"`
	Metadata store.Metadata `json:"metadata"`
}

func decodeRideList(t *testing.T, rr *httptest.ResponseRecorder) rideListResponse {
	t.Helper()

	var got rideListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list response: %v (body: %s)", err, rr.Body.String())
	}
	return got
}

// TestRideCommandHandlers covers the three decision-phase commands and the
// error mapping each rides on: happy paths (burn/unlock/lock), the
// invalid-state 409 (command outside the transition graph), the
// invalid-phase 409 (command in the wrong phase), and no-next-match.
func TestRideCommandHandlers(t *testing.T) {
	requireDB(t)

	// decidedFixture: open round 3 + a closed match inside it, so the round
	// derives DecisionPhase; returns the closed match's id. Player 1 rides
	// for team 1 throughout.
	decidedFixture := func(t *testing.T) int {
		t.Helper()
		reset(t)
		return plantDecidedMatch(t)
	}

	command := func(method, url string) *httptest.ResponseRecorder {
		app := newTestApplication()
		req := httptest.NewRequest(method, url, nil)
		rr := httptest.NewRecorder()
		app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
		return rr
	}

	assertResponse := func(t *testing.T, rr *httptest.ResponseRecorder, wantCode int, wantBody ...string) {
		t.Helper()
		if rr.Code != wantCode {
			t.Fatalf("got status %d, want %d (body: %s)", rr.Code, wantCode, rr.Body.String())
		}
		for _, fragment := range wantBody {
			if !strings.Contains(rr.Body.String(), fragment) {
				t.Errorf("body missing %q (body: %s)", fragment, rr.Body.String())
			}
		}
	}

	t.Run("burn a won_pending ride", func(t *testing.T) {
		closedMatch := decidedFixture(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, closedMatch, "won_pending", "90", 1)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/burn"),
			http.StatusOK, `"state": "burned"`, `"version": 2`)
	})

	t.Run("unlock a won_pending ride forfeits nothing visible but clears acc", func(t *testing.T) {
		closedMatch := decidedFixture(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, closedMatch, "won_pending", "90", 1)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/unlock"),
			http.StatusOK, `"state": "unlocked"`, `"version": 2`)
	})

	t.Run("lock a won_pending ride continues onto the team's next match", func(t *testing.T) {
		closedMatch := decidedFixture(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, closedMatch, "won_pending", "90", 1)

		// Team 1's next upcoming match is match 5 (round 3, starts +2 days)
		// — the earliest the NextForTeam query can find.
		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/lock"),
			http.StatusOK, `"state": "locked"`, `"match_id": 5`)
	})

	t.Run("burn outside the transition graph is the invalid-state 409", func(t *testing.T) {
		// A locked ride cannot burn: locked is only leaving via won_pending
		// or lost during the match phase. The command reaches the DB gate
		// only through the domain — here the domain refuses first; either
		// way the handler must answer 409 invalid state, never 500.
		closedMatch := decidedFixture(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, closedMatch, "locked", "0", 0)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/burn"),
			http.StatusConflict, "not allowed for a ride in current state")
	})

	t.Run("unlock in action phase is the invalid-phase 409", func(t *testing.T) {
		// Round 3 without a decided match stays in ActionPhase; unlock is a
		// decision-phase command.
		reset(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, 5, "won_pending", "90", 1)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/unlock"),
			http.StatusConflict, "not allowed while the round is in current phase")
	})

	t.Run("burn in action phase is the invalid-phase 409", func(t *testing.T) {
		reset(t)
		playerID := plantPlayerRow(t, 1)
		rideID := plantRideRow(t, playerID, 1, 5, "locked", "0", 0)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/burn"),
			http.StatusConflict, "not allowed while the round is in current phase")
	})

	t.Run("lock without a next match is the no-next-match 409", func(t *testing.T) {
		// Team 3 has no other match anywhere: the ride's continuation has no
		// destination, which is a schedule conflict, not a state conflict.
		closedMatch := decidedFixture(t)
		if _, err := testPool.Exec(context.Background(), `INSERT INTO teams (name, logo, description) VALUES ('AEK', 'https://x.example/aek.png', 'Athens')`); err != nil {
			t.Fatalf("plant team 3: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at, ended_at)
			VALUES (1, 3, 3, 2, 1.75, 2.20, 70, 69, 'closed', now() - interval '3 hours', now() - interval '2 hours')
		`); err != nil {
			t.Fatalf("plant team-3 match: %v", err)
		}
		// Lock continues on ride.TeamID, so the ride itself must belong to
		// team 3 — the player row is just the FK parent.
		playerID := plantPlayerRow(t, 3)
		rideID := plantRideRow(t, playerID, 3, closedMatch, "won_pending", "90", 1)

		assertResponse(t, command(http.MethodPost, "/v1/rides/"+strconv.Itoa(rideID)+"/lock"),
			http.StatusConflict, "the ride cannot be continued because the team has no upcoming matches")
	})

	t.Run("command on an unknown ride is 404", func(t *testing.T) {
		decidedFixture(t)

		for _, url := range []string{"/v1/rides/999/lock", "/v1/rides/999/burn", "/v1/rides/999/unlock"} {
			assertResponse(t, command(http.MethodPost, url),
				http.StatusNotFound, "could not be found")
		}
	})
}
