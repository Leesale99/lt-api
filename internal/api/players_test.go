package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/game"
)

// Template for all Phase 00 handler tests: fresh app per subtest (no shared
// state), table of {request -> want code + body fragments}.
//
// newTestApplication passes a nil pool: Teams is now Postgres-backed and
// Phase 00 tests have no database. Subtests that would reach TeamStore must
// set needsDB and are skipped until the integration-test task (Phase 01).

func newTestApplication() *Application {
	return &Application{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  game.NewStore(nil),
	}
}

func TestShowPlayerHandler(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
		needsDB  bool
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
	tests := []struct {
		name     string
		url      string
		body     string
		wantCode int
		wantBody []string
		// needsDB marks cases that reach TeamStore (Postgres-backed).
		// They are skipped until the Phase 01 integration-test task.
		needsDB bool
	}{
		{
			name:     "valid registration",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"John Doe","favorite_team_id":1}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 2`, `"name": "John Doe"`, `"favorite_team_id": 1`},
			needsDB:  true,
		},
		{
			name:     "season takes effect from URL, not body",
			url:      "/v1/seasons/2/players",
			body:     `{"name":"Jane Doe","favorite_team_id":2}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 2`, `"name": "Jane Doe"`, `"favorite_team_id": 2`},
			needsDB:  true,
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
			needsDB:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsDB {
				t.Skip("Teams is Postgres-backed; needs a real DB (Phase 01 integration-test task)")
			}

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
