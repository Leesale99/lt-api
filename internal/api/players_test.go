package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/game"
)

// Template for all handler tests: fresh app + fresh fixture per subtest
// (reset() re-seeds the canonical rows with fixed IDs), table of
// {request -> want code + body fragments}.
//
// newTestApplication wires the real test-database pool set up by TestMain.

func newTestApplication() *Application {
	return &Application{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Store:  game.NewStore(testPool),
	}
}

func TestShowPlayerHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "existing player",
			url:      "/v1/players/1",
			wantCode: http.StatusOK,
			wantBody: []string{`"season_id": 2`, `"favorite_team_id": 1`},
		},
		{
			name:     "unknown player",
			url:      "/v1/players/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/players/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/players/abc",
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

func TestCreatePlayerHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid registration",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe","favorite_team_id":1}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 2`, `"name": "John Doe"`, `"favorite_team_id": 1`},
		},
		{
			name:     "season takes effect from URL, not body",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"Jane Doe","favorite_team_id":2}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 2`, `"name": "Jane Doe"`, `"favorite_team_id": 2`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999/players",
			body:     `{"name":"John Doe","favorite_team_id":1}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric season id",
			url:      "/v1/seasons/abc/players",
			body:     `{"name":"John Doe","favorite_team_id":1}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/2/players",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/2/players",
			body:     `{"favorite_team_id":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe","favorite_team_id":1,"nickname":"x"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "missing name",
			url:      "/v1/seasons/2/players",
			body:     `{"favorite_team_id":1}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "empty name",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"","favorite_team_id":1}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "name too long",
			url:      "/v1/seasons/2/players",
			body:     fmt.Sprintf(`{"name":"%s","favorite_team_id":1}`, strings.Repeat("a", 201)),
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "missing favorite team",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"favorite_team_id"},
		},
		{
			name:     "zero favorite team",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe","favorite_team_id":0}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"favorite_team_id"},
		},
		{
			name:     "team must exist",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe","favorite_team_id":999}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"favorite_team_id", "existing team"},
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

// Canonical player: ID 1, season 2, favorite team 1, version 1.
// The season segment is scope-only: updating a player under a season it does
// not belong to is a 404, not a season reassignment (ADR pending).
func TestUpdatePlayerHandler(t *testing.T) {
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
			name:     "partial update, name only",
			url:      "/v1/seasons/2/players/1",
			body:     `{"name":"Ioannis Bourousis"}`,
			wantCode: http.StatusOK,
			wantBody: []string{
				`"name": "Ioannis Bourousis"`,
				`"favorite_team_id": 1`,
				`"season_id": 2`,
				`"version": 2`,
			},
		},
		{
			name:     "partial update, favorite team only",
			url:      "/v1/seasons/2/players/1",
			body:     `{"favorite_team_id":2}`,
			wantCode: http.StatusOK,
			wantBody: []string{
				`"name": "Sasha Vezenkov"`,
				`"favorite_team_id": 2`,
				`"season_id": 2`,
				`"version": 2`,
			},
		},
		{
			name:     "full update",
			url:      "/v1/seasons/2/players/1",
			body:     `{"name":"Ioannis Bourousis","favorite_team_id":2}`,
			wantCode: http.StatusOK,
			wantBody: []string{
				`"name": "Ioannis Bourousis"`,
				`"favorite_team_id": 2`,
				`"season_id": 2`,
				`"version": 2`,
			},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/2/players/1",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/2/players/1",
			body:     `{"name":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/2/players/1",
			body:     `{"name":"X","nickname":"y"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "empty name",
			url:      "/v1/seasons/2/players/1",
			body:     `{"name":""}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "name too long",
			url:      "/v1/seasons/2/players/1",
			body:     fmt.Sprintf(`{"name":"%s"}`, strings.Repeat("a", 201)),
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "zero favorite team",
			url:      "/v1/seasons/2/players/1",
			body:     `{"favorite_team_id":0}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"favorite_team_id"},
		},
		{
			name:     "team must exist",
			url:      "/v1/seasons/2/players/1",
			body:     `{"favorite_team_id":999}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"favorite_team_id", "existing team"},
		},
		{
			name:     "wrong season in URL",
			url:      "/v1/seasons/1/players/1",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "unknown season in URL",
			url:      "/v1/seasons/999/players/1",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "unknown player",
			url:      "/v1/seasons/2/players/999",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero player id",
			url:      "/v1/seasons/2/players/0",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric player id",
			url:      "/v1/seasons/2/players/abc",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "matching version header",
			url:      "/v1/seasons/2/players/1",
			headers:  map[string]string{"X-Expected-Version": "1"},
			body:     `{"name":"Ioannis Bourousis"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"version": 2`},
		},
		{
			name:     "stale version header",
			url:      "/v1/seasons/2/players/1",
			headers:  map[string]string{"X-Expected-Version": "9"},
			body:     `{"name":"Ioannis Bourousis"}`,
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
