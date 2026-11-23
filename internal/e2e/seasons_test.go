package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowSeasonHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "existing season",
			url:      "/v1/seasons/1",
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "in_progress"`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/abc",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
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
}

func TestCreateSeasonHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			// Status is server-assigned on create (always "created"); the
			// client cannot set it, so it is an unknown key like any other.
			name:     "status in the body is rejected",
			body:     `{"status":"in_progress"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			// An empty object creates a season in the created state.
			name:     "empty object creates a created season",
			body:     `{}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"status": "created"`},
		},
		{
			name:     "empty body",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			body:     `{"status":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			body:     `{"year":2026}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			var reader io.Reader
			if tt.body != "" {
				reader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/seasons", reader)
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

func TestUpdateSeasonHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		headers  map[string]string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid update",
			url:      "/v1/seasons/2",
			body:     `{"status":"closed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "closed"`, `"version": 2`},
		},
		{
			// Lowercase is the wire contract: vocabulary values are matched
			// exactly, there is no server-side normalization.
			name:     "uppercase status is rejected",
			url:      "/v1/seasons/2",
			body:     `{"status":"CLOSED"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "must be one of: created, open, in_progress, closed"},
		},
		{
			name: "regressing the lifecycle is rejected",
			// Advisory check (seasons_freeze_gate backs it up in the DB):
			// in_progress cannot go back to open. Season 1 is the canonical
			// in_progress season; season 2 is created, so its transitions
			// are all forward.
			url:      "/v1/seasons/1",
			body:     `{"status":"open"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "earlier stage of the season lifecycle"},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/2",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/2",
			body:     `{"status":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/2",
			body:     `{"status":"open","year":2026}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "empty object is a no-op",
			url:      "/v1/seasons/2",
			body:     `{}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "created"`, `"version": 2`},
		},
		{
			name:     "unknown status",
			url:      "/v1/seasons/2",
			body:     `{"status":"playoff"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999",
			body:     `{"status":"open"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/0",
			body:     `{"status":"open"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/abc",
			body:     `{"status":"open"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "matching version header",
			url:      "/v1/seasons/2",
			headers:  map[string]string{"X-Expected-Version": "1"},
			body:     `{"status":"closed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"version": 2`},
		},
		{
			name:     "stale version header",
			url:      "/v1/seasons/2",
			headers:  map[string]string{"X-Expected-Version": "9"},
			body:     `{"status":"closed"}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"edit conflict"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			var reader io.Reader
			if tt.body != "" {
				reader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(http.MethodPatch, tt.url, reader)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
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

func TestCreateSeasonHandlerLocation(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/v1/seasons/3" {
		t.Errorf("Location = %q, want %q", got, "/v1/seasons/3")
	}
}

func TestDeleteSeasonHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name: "in_progress season is lifecycle-gated",
			// Season 1 is the canonical in_progress season (ADR-007).
			url:      "/v1/seasons/1",
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
		},
		{
			name: "created season can be deleted",
			// Season 2 is created with nothing started; the delete cascades
			// its rounds, matches and players.
			url:      "/v1/seasons/2",
			wantCode: http.StatusOK,
			wantBody: []string{"successfully deleted"},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/abc",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			req := httptest.NewRequest(http.MethodDelete, tt.url, nil)
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

// TestDeleteCreatedSeasonHandler covers the permitted path of ADR-007: a
// 'created' season can be hard-deleted, the delete cascades to its rounds,
// and the trigger does not interfere.
func TestDeleteCreatedSeasonHandler(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	id := strings.TrimPrefix(rr.Header().Get("Location"), "/v1/seasons/")

	// A round under the new season proves the cascade fires on delete. It
	// stays created: the lifecycle gates (000008) refuse an open round under
	// a season that is not live, and this season is freshly created.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, 1, 'created')`, id); err != nil {
		t.Fatalf("seed round: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/seasons/"+id, nil)
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("delete: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "successfully deleted") {
		t.Errorf("body missing confirmation (body: %s)", rr.Body.String())
	}

	var seasons, rounds int
	err := testPool.QueryRow(context.Background(),
		`SELECT
			(SELECT count(*) FROM schedule.seasons WHERE id = $1),
			(SELECT count(*) FROM schedule.rounds WHERE season_id = $1)`, id).Scan(&seasons, &rounds)
	if err != nil {
		t.Fatal(err)
	}
	if seasons != 0 || rounds != 0 {
		t.Fatalf("season %s not fully deleted: %d season(s), %d round(s) remain", id, seasons, rounds)
	}
}

// TestDeleteSeasonRefusalKeepsSeasonPlayers pins the all-or-nothing shape of
// the season delete (R-SCHEDULE-027): the composition deletes the season's
// players through game BEFORE the season row, so a refusal that arrives
// afterwards must roll the player delete back with it. Before the transaction,
// a refused delete left the season alive and its registrations gone — the old
// single-statement cascade could not do that.
//
// Both refusals the path has are here: schedule's own delete gate
// (seasons_delete_gate, a BEFORE DELETE trigger — the shape that made the
// defect possible) and game's rides_player_id_fkey (the refusal that already
// sits at the players step). Each case plants its players first, so a lost
// registration is visible, and reads them back afterwards.
func TestDeleteSeasonRefusalKeepsSeasonPlayers(t *testing.T) {
	requireDB(t)

	const seasonID = 2 // the created season: only a planted fact can refuse it

	tests := []struct {
		name string
		// plant arranges the refusal on the season and returns the ids of
		// the registrations that must survive it.
		plant func(t *testing.T) []int
	}{
		{
			// seasons_delete_gate (R-SCHEDULE-023) fires on the season DELETE
			// itself, i.e. after the players step: the registrations must come
			// back with the rolled-back transaction.
			name: "season delete gate refuses after the players step",
			plant: func(t *testing.T) []int {
				t.Helper()

				players := plantSeasonPlayers(t, seasonID)

				var roundID int
				if err := testPool.QueryRow(context.Background(),
					`SELECT id FROM schedule.rounds WHERE season_id = $1 ORDER BY number LIMIT 1`, seasonID).Scan(&roundID); err != nil {
					t.Fatalf("pick round: %v", err)
				}

				// The started match is what the gate reads (seasons_block_delete
				// -> season_has_started_match); inserted directly, as the gates'
				// own repository tests do.
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at)
					VALUES ($1, $2, 1, 2, 1.5, 2.5, 50, 49, 'in_progress', now() - interval '1 hour')`,
					seasonID, roundID); err != nil {
					t.Fatalf("plant started match: %v", err)
				}

				return players
			},
		},
		{
			// rides_player_id_fkey (game-internal, RESTRICT): the player delete
			// trips 23503 before the season row is touched. Same requirement —
			// the refusal cannot take the registrations with it.
			name: "a ride on a registration refuses the whole delete",
			plant: func(t *testing.T) []int {
				t.Helper()

				players := plantSeasonPlayers(t, seasonID)

				var matchID int
				if err := testPool.QueryRow(context.Background(),
					`SELECT id FROM schedule.matches WHERE season_id = $1 ORDER BY id LIMIT 1`, seasonID).Scan(&matchID); err != nil {
					t.Fatalf("pick match: %v", err)
				}

				plantRideRow(t, players[0], 1, matchID, "locked", "0", 0)

				return players
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			players := tt.plant(t)

			req := httptest.NewRequest(http.MethodDelete, "/v1/seasons/2", nil)
			rr := httptest.NewRecorder()
			app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

			if rr.Code != http.StatusConflict {
				t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusConflict, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "referenced by other records") {
				t.Errorf("body missing the in-use refusal (body: %s)", rr.Body.String())
			}

			// The refusal rolled the whole composition back: the season is
			// still there...
			var seasons int
			if err := testPool.QueryRow(context.Background(),
				`SELECT count(*) FROM schedule.seasons WHERE id = $1`, seasonID).Scan(&seasons); err != nil {
				t.Fatal(err)
			}
			if seasons != 1 {
				t.Fatalf("season %d is gone after a refused delete, want it to survive", seasonID)
			}

			// ...and so are the registrations the players step had already
			// deleted (the defect this test pins).
			for _, playerID := range players {
				var exists bool
				if err := testPool.QueryRow(context.Background(),
					`SELECT EXISTS (SELECT 1 FROM game.players WHERE id = $1)`, playerID).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if !exists {
					t.Errorf("player %d was deleted by a refused season delete, want it to survive", playerID)
				}
			}
		})
	}
}

// plantSeasonPlayers registers two players in the season and returns their
// ids. Two, not one: a lost registration is a defect either way, and the
// statement deletes by season_id, so more than one row makes a partial
// delete visible too.
func plantSeasonPlayers(t *testing.T, seasonID int) []int {
	t.Helper()

	names := []string{"Refused Delete One", "Refused Delete Two"}
	ids := make([]int, 0, len(names))

	for _, name := range names {
		var id int
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO game.players (season_id, favorite_team_id, name)
			VALUES ($1, 1, $2)
			RETURNING id`, seasonID, name).Scan(&id); err != nil {
			t.Fatalf("plant player: %v", err)
		}
		ids = append(ids, id)
	}

	return ids
}

func TestListSeasonsHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "no filters",
			url:      "/v1/seasons",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"status": "in_progress"`, `"status": "created"`},
		},
		{
			name:     "filter by id",
			url:      "/v1/seasons?id=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "created"`, `"id": 2`},
		},
		{
			name:     "filter by status",
			url:      "/v1/seasons?status=in_progress",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "in_progress"`},
		},
		{
			// Lowercase is the wire contract: vocabulary values are matched
			// exactly, there is no server-side normalization.
			name:     "uppercase status filter is rejected",
			url:      "/v1/seasons?status=IN_PROGRESS",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "must be one of: created, open, in_progress, closed"},
		},
		{
			name:     "combined id and status",
			url:      "/v1/seasons?id=1&status=in_progress",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "in_progress"`},
		},
		{
			name:     "combined id and status with no match",
			url:      "/v1/seasons?id=1&status=open",
			wantCode: http.StatusOK,
			// zero Metadata is omitzero-dropped, so an empty page carries no total_records
			wantBody: []string{`"seasons": []`},
		},
		{
			name:     "id zero is no filter",
			url:      "/v1/seasons?id=0",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			name:     "pagination honored",
			url:      "/v1/seasons?page=2&page_size=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"status": "created"`, `"current_page": 2`},
		},
		{
			name:     "descending sort",
			url:      "/v1/seasons?sort=-id",
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "created"`},
		},
		{
			name:     "invalid status filter",
			url:      "/v1/seasons?status=playoff",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "name sort rejected by safelist",
			url:      "/v1/seasons?sort=name",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"sort", "invalid sort value"},
		},
		{
			name:     "page zero",
			url:      "/v1/seasons?page=0",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page", "must be greater than zero"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
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
}
