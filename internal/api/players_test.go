package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
)

// Template for all handler tests: fresh app + fresh fixture per subtest
// (reset() re-seeds the canonical rows with fixed IDs), table of
// {request -> want code + body fragments}.
//
// newTestApplication wires the real test-database pool set up by TestMain.

func newTestApplication() *Application {
	return &Application{
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Game:     game.NewStore(testPool),
		Identity: identity.NewStore(testPool),
		Mailer:   nopMailer{},
	}
}

// nopMailer stands in for the real SMTP client: handlers call Send, nothing
// leaves the process, and the background goroutine has nothing to panic on.
type nopMailer struct{}

func (nopMailer) Send(recipient string, templateFile string, data any) error {
	return nil
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

// TestCreatePlayerDuplicateRegistration covers the ErrDuplicateRecord mapping
// on the create path: one player per user per season (players_season_id_user_id_key),
// so a second POST with the same authenticated user is a 409.
func TestCreatePlayerDuplicateRegistration(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons/2/players",
		strings.NewReader(`{"name":"Sasha Vezenkov","favorite_team_id":1}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusCreated {
		t.Fatalf("first registration: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/seasons/2/players",
		strings.NewReader(`{"name":"Other Name","favorite_team_id":2}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate registration: got status %d, want %d (body: %s)", rr.Code, http.StatusConflict, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unique values") {
		t.Errorf("body missing duplicate message (body: %s)", rr.Body.String())
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

func TestDeletePlayerHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			// player 1 lives in season 2 — deleting under season 1 must 404
			// (season scoping is inside the DELETE's WHERE clause) and leave
			// the row in place.
			name:     "player in another season",
			url:      "/v1/seasons/1/players/1",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "unknown player",
			url:      "/v1/seasons/2/players/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero player id",
			url:      "/v1/seasons/2/players/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric player id",
			url:      "/v1/seasons/2/players/abc",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero season id",
			url:      "/v1/seasons/0/players/1",
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

			// Nothing in the error matrix may ever delete the fixture player.
			var n int
			err := testPool.QueryRow(context.Background(),
				`SELECT count(*) FROM players WHERE id = 1`).Scan(&n)
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("fixture player 1 missing after failed delete")
			}
		})
	}
}

// TestDeletePlayerHandlerHappyPath covers the success case: the fixture
// player is unreferenced, so the delete must 200 and the row must be gone
// afterwards.
func TestDeletePlayerHandlerHappyPath(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodDelete, "/v1/seasons/2/players/1", nil)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "successfully deleted") {
		t.Errorf("body missing confirmation (body: %s)", rr.Body.String())
	}

	var n int
	err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM players WHERE id = 1`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("player 1 still in the database after delete")
	}
}

// secondPlayerSQL inserts a second fixture player for pagination cases
// ('Nikola Milutinov' sorts after 'Sasha Vezenkov' either direction).
const secondPlayerSQL = `
	INSERT INTO players (season_id, favorite_team_id, name)
	VALUES (2, 2, 'Nikola Milutinov');
`

// TestListPlayersHandler covers GET /v1/players: filter combinations
// (name / favorite_team_id / both), sort + pagination, and the filter
// validation errors. The fixture has a single player (id 1, 'Sasha
// Vezenkov', season 2, favorite team 1); pagination cases insert a second
// player inline since two rows are needed.
func TestListPlayersHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		extraSQL string
		wantCode int
		wantBody []string
	}{
		{
			name:     "unfiltered list",
			url:      "/v1/players",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Sasha Vezenkov"`, `"total_records": 1`, `"last_page": 1`},
		},
		{
			name:     "filter by name fragment (case-insensitive)",
			url:      "/v1/players?name=sasha",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Sasha Vezenkov"`, `"total_records": 1`},
		},
		{
			name:     "name filter matches nothing",
			url:      "/v1/players?name=nowhere",
			wantCode: http.StatusOK,
			wantBody: []string{`"players": []`, `"metadata": {}`},
		},
		{
			name:     "filter by favorite team",
			url:      "/v1/players?favorite_team_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Sasha Vezenkov"`, `"total_records": 1`},
		},
		{
			name:     "favorite team filter matches nothing",
			url:      "/v1/players?favorite_team_id=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"players": []`, `"metadata": {}`},
		},
		{
			name:     "combined name and favorite team filters (both match)",
			url:      "/v1/players?name=vezenkov&favorite_team_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Sasha Vezenkov"`, `"total_records": 1`},
		},
		{
			name:     "combined filters (name matches, team does not)",
			url:      "/v1/players?name=sasha&favorite_team_id=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"players": []`, `"metadata": {}`},
		},
		{
			name:     "sorted by name descending, first page",
			url:      "/v1/players?sort=-name&page_size=1",
			extraSQL: secondPlayerSQL,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Sasha Vezenkov"`, `"current_page": 1`, `"last_page": 2`},
		},
		{
			name:     "second page",
			url:      "/v1/players?sort=-name&page_size=1&page=2",
			extraSQL: secondPlayerSQL,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Nikola Milutinov"`, `"current_page": 2`},
		},
		{
			// count(*) OVER() only materializes per returned row: an offset
			// past all matching rows yields no rows, hence empty metadata.
			name:     "page beyond the last",
			url:      "/v1/players?page=99",
			wantCode: http.StatusOK,
			wantBody: []string{`"players": []`, `"metadata": {}`},
		},
		{
			name:     "page zero",
			url:      "/v1/players?page=0",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page", "must be greater than zero"},
		},
		{
			name:     "page_size zero",
			url:      "/v1/players?page_size=0",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page_size", "must be greater than zero"},
		},
		{
			name:     "page_size over the maximum",
			url:      "/v1/players?page_size=101",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page_size", "must be a maximum of 100"},
		},
		{
			name:     "page not a number",
			url:      "/v1/players?page=abc",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page", "must be an integer value"},
		},
		{
			name:     "sort not in the safelist",
			url:      "/v1/players?sort=season_id",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"sort", "invalid sort value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			if tt.extraSQL != "" {
				if _, err := testPool.Exec(context.Background(), tt.extraSQL); err != nil {
					t.Fatalf("extra seed: %v", err)
				}
			}
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
