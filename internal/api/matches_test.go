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
			wantBody: []string{`"status": "open"`},
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
			app.routes().ServeHTTP(rr, req)

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
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
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
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"starts_at": "2030-01-01T12:00:00Z"`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric season id",
			url:      "/v1/seasons/abc/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3}}`,
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
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3},"venue":"Athens"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "missing round",
			url:      "/v1/seasons/1/matches",
			body:     `{"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"round_id"},
		},
		{
			name:     "zero odds",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":0,"away":3}}`,
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
			body:     `{"round_id":3,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3},` + futureStartsAt + `}`,
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
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3}}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"starts_at", "must be provided"},
		},
		{
			name:     "starts_at in the past",
			url:      "/v1/seasons/1/matches",
			body:     `{"round_id":1,"home_team_id":1,"away_team_id":2,"status":"open","odds":{"home":2,"away":3},"starts_at":"2020-01-01T12:00:00Z"}`,
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
			app.routes().ServeHTTP(rr, req)

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
			name:     "open match can be deleted",
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
			app.routes().ServeHTTP(rr, req)

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
	app.routes().ServeHTTP(rr, req)

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
