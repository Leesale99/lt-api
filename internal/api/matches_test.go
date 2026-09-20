package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fixed future timestamp for happy-path bodies: starts_at must be in the
// future relative to the handler's time.Now(), so any far-future date works
// and keeps the cases deterministic.
const futureStartsAt = `"starts_at":"2030-01-01T12:00:00Z"`

func TestShowMatchHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "played match carries score",
			url:      "/v1/matches/1",
			wantCode: http.StatusOK,
			wantBody: []string{`"home_team_id": 1`, `"score"`},
		},
		{
			name:     "unplayed match",
			url:      "/v1/matches/2",
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "created"`},
		},
		{
			name:     "unknown match",
			url:      "/v1/matches/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/matches/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/matches/abc",
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

func TestCreateMatchHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid unplayed match",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 1`, `"round_id": 1`, `"home_team_id": 1`},
		},
		{
			name:     "valid played match",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"closed","odds":{"home":2,"away":3},"score":{"home":80,"away":75},` + futureStartsAt + `}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"status": "closed"`, `"score"`},
		},
		{
			name:     "happy path with starts_at",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"starts_at": "2030-01-01T12:00:00Z"`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric season id",
			url:      "/v1/seasons/abc/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/1/matches",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3},"venue":"Athens"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "missing round",
			url:      "/v1/seasons/1/matches",
			body:     `{"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"round_id"},
		},
		{
			name:     "zero odds",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":0,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"odds"},
		},
		{
			name:     "negative score",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"closed","odds":{"home":2,"away":3},"score":{"home":-1,"away":75}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"score"},
		},
		{
			name:     "unknown status",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"pending","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "round in wrong season",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":3,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"round_id", "must belong to a season id 1"},
		},
		{
			name:     "partial score",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"closed","odds":{"home":2,"away":3},"score":{"home":80},` + futureStartsAt + `}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"score", "must contain both home and away values or neither"},
		},
		{
			name:     "closed without score",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"closed","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"score", "must be provided when the match is in progress or closed"},
		},
		{
			name:     "postponed with score",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"postponed","odds":{"home":2,"away":3},"score":{"home":80,"away":75},` + futureStartsAt + `}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"score", "must not be set before the match is in progress or closed"},
		},
		{
			name:     "starts_at missing",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"starts_at", "must be provided"},
		},
		{
			name:     "starts_at in the past",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"created","odds":{"home":2,"away":3},"starts_at":"2020-01-01T12:00:00Z"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"starts_at", "must be in the future"},
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
			req := httptest.NewRequest(http.MethodPost, tt.url, reader)
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

func TestDeleteMatchHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name: "closed match in an open round can be deleted",
			// Deliberate exposure (mirrors ADR-007 one level down): the gate
			// lives on rounds, so a closed match inside a still-open round is
			// deletable. Match 1 is the canonical closed match.
			url:      "/v1/seasons/1/matches/1",
			wantCode: http.StatusOK,
			wantBody: []string{"successfully deleted"},
		},
		{
			name:     "created match can be deleted",
			url:      "/v1/seasons/1/matches/2",
			wantCode: http.StatusOK,
			wantBody: []string{"successfully deleted"},
		},
		{
			name:     "unknown match",
			url:      "/v1/seasons/1/matches/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/1/matches/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/1/matches/abc",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name: "match in another season",
			// Both canonical matches belong to season 1.
			url:      "/v1/seasons/2/matches/1",
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

// TestDeleteMatchRemovesRow asserts the happy path is a real hard delete, not
// just a 200.
func TestDeleteMatchRemovesRow(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodDelete, "/v1/seasons/1/matches/1", nil)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var count int
	err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM matches WHERE id = 1`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("match 1 still exists after delete")
	}
}

func TestListMatchesHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "no filters",
			url:      "/v1/matches",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"round_id": 1`},
		},
		{
			name:     "filter by season_id",
			url:      "/v1/matches?season_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"season_id": 1`},
		},
		{
			name:     "filter by season_id no match",
			url:      "/v1/matches?season_id=2",
			wantCode: http.StatusOK,
			// zero Metadata is omitzero-dropped, so an empty page carries no total_records
			wantBody: []string{`"matches": []`},
		},
		{
			name:     "season_id zero is no filter",
			url:      "/v1/matches?season_id=0",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			name:     "filter by round_id",
			url:      "/v1/matches?round_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			name:     "filter by round_id no match",
			url:      "/v1/matches?round_id=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"matches": []`},
		},
		{
			name:     "filter by status created",
			url:      "/v1/matches?status=created",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "created"`},
		},
		{
			name:     "filter by status closed",
			url:      "/v1/matches?status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "closed"`, `"score"`},
		},
		{
			name:     "filter by status no match",
			url:      "/v1/matches?status=postponed",
			wantCode: http.StatusOK,
			wantBody: []string{`"matches": []`},
		},
		{
			name:     "uppercase status filter is normalized",
			url:      "/v1/matches?status=CLOSED",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`},
		},
		{
			name:     "combined season_id, round_id and status",
			url:      "/v1/matches?season_id=1&round_id=1&status=created",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "created"`},
		},
		{
			name:     "combined filters with no match",
			url:      "/v1/matches?season_id=2&round_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"matches": []`},
		},
		{
			name:     "invalid status filter",
			url:      "/v1/matches?status=finishing",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "sort by starts_at ascending returns the earlier match first",
			url:      "/v1/matches?page_size=1&sort=starts_at",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"status": "closed"`},
		},
		{
			name:     "sort by starts_at descending returns the later match first",
			url:      "/v1/matches?page_size=1&sort=-starts_at",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"status": "created"`},
		},
		{
			name:     "sort rejected by safelist",
			url:      "/v1/matches?sort=round_id",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"sort", "invalid sort value"},
		},
		{
			name:     "page zero",
			url:      "/v1/matches?page=0",
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

// TestUpdateMatchHandler covers the ADR-008 freeze rule on the match update
// path: postpone and regression are allowed only before the match starts.
// The time fact is data (starts_at), so cases seed started matches via SQL
// instead of controlling the clock.
func TestUpdateMatchHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		seed     string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "odds update on a created match",
			url:      "/v1/seasons/1/matches/2",
			body:     `{"odds":{"home":3.0,"away":1.7}}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"odds": {`, `"version": 2`},
		},
		{
			name:     "created match can be postponed before it starts",
			url:      "/v1/seasons/1/matches/2",
			body:     `{"status":"postponed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "postponed"`},
		},
		{
			name: "started match cannot be postponed",
			// Match 3: created but already past its starts_at (the advisory
			// rank check cannot see time, so this reaches the DB gate).
			url:      "/v1/seasons/1/matches/3",
			seed:     `INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at) VALUES (1, 2, 1, 2, 1.5, 2.5, 'created', now() - interval '1 hour')`,
			body:     `{"status":"postponed"}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"a match has already started"},
		},
		{
			name:     "started match cannot return to created",
			url:      "/v1/seasons/1/matches/3",
			seed:     `INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, status, starts_at) VALUES (1, 2, 1, 2, 1.5, 2.5, 50, 49, 'in_progress', now() - interval '1 hour')`,
			body:     `{"status":"created","score":{}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "earlier stage of the match lifecycle"},
		},
		{
			name:     "closed match is terminal",
			url:      "/v1/seasons/1/matches/1",
			body:     `{"status":"in_progress"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "cannot be changed after the match is closed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()

			if tt.seed != "" {
				if _, err := testPool.Exec(context.Background(), tt.seed); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			req := httptest.NewRequest(http.MethodPatch, tt.url, strings.NewReader(tt.body))
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

// TestPostponeStartedMatchFrozen asserts the DB gate actually left the row
// untouched: the frozen write must be a full no-op, not a partial one.
func TestPostponeStartedMatchFrozen(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		 VALUES (1, 2, 1, 2, 1.5, 2.5, 'created', now() - interval '1 hour')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/seasons/1/matches/3",
		strings.NewReader(`{"status":"postponed","odds":{"home":9.9,"away":9.9}}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusConflict {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusConflict, rr.Body.String())
	}

	var status string
	var homeOdds float64
	var version int
	err := testPool.QueryRow(context.Background(),
		`SELECT status, home_odds, version FROM matches WHERE id = 3`).Scan(&status, &homeOdds, &version)
	if err != nil {
		t.Fatal(err)
	}
	if status != "created" || homeOdds != 1.5 || version != 1 {
		t.Fatalf("frozen update partially applied: status=%q home_odds=%v version=%d", status, homeOdds, version)
	}
}
