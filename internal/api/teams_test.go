package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowTeamHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "existing team",
			url:      "/v1/teams/1",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`},
		},
		{
			name:     "unknown team",
			url:      "/v1/teams/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/teams/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/teams/abc",
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

func TestCreateTeamHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid team",
			body:     `{"name":"Panathinaikos","logo":"http://example.com/pao.png","description":"Athens club"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"name": "Panathinaikos"`},
		},
		{
			name:     "empty body",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			body:     `{"name":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			body:     `{"name":"X","logo":"http://example.com/x.png","description":"d","city":"Athens"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "missing name",
			body:     `{"logo":"http://example.com/x.png","description":"d"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name"},
		},
		{
			name:     "missing logo",
			body:     `{"name":"X","description":"d"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"logo"},
		},
		{
			name:     "logo not a URL",
			body:     `{"name":"X","logo":"not-a-url","description":"d"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"logo"},
		},
		{
			name:     "logo wrong extension",
			body:     `{"name":"X","logo":"http://example.com/x.gif","description":"d"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"logo"},
		},
		{
			name:     "missing description",
			body:     `{"name":"X","logo":"http://example.com/x.png"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"description"},
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
			req := httptest.NewRequest(http.MethodPost, "/v1/teams", reader)
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

func TestListTeamsHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "unfiltered list",
			url:      "/v1/teams",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`, `"name": "Real Madrid"`, `"total_records": 2`, `"last_page": 1`},
		},
		{
			name:     "filter by name fragment (case-insensitive)",
			url:      "/v1/teams?name=olympi",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`, `"total_records": 1`},
		},
		{
			name:     "filter matches nothing",
			url:      "/v1/teams?name=nowhere",
			wantCode: http.StatusOK,
			wantBody: []string{`"teams": []`, `"metadata": {}`},
		},
		{
			name:     "sorted by name descending, first page",
			url:      "/v1/teams?sort=-name&page_size=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Real Madrid"`, `"current_page": 1`, `"last_page": 2`},
		},
		{
			name:     "second page",
			url:      "/v1/teams?sort=-name&page_size=1&page=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Olympiacos"`, `"current_page": 2`},
		},
		{
			// count(*) OVER() only materializes per returned row: an offset
			// past all matching rows yields no rows, hence empty metadata.
			name:     "page beyond the last",
			url:      "/v1/teams?page=99",
			wantCode: http.StatusOK,
			wantBody: []string{`"teams": []`, `"metadata": {}`},
		},
		{
			name:     "page zero",
			url:      "/v1/teams?page=0",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page", "must be greater than zero"},
		},
		{
			name:     "page_size zero",
			url:      "/v1/teams?page_size=0",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page_size", "must be greater than zero"},
		},
		{
			name:     "page_size over the maximum",
			url:      "/v1/teams?page_size=101",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page_size", "must be a maximum of 100"},
		},
		{
			name:     "page not a number",
			url:      "/v1/teams?page=abc",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"page", "must be an integer value"},
		},
		{
			name:     "sort not in the safelist",
			url:      "/v1/teams?sort=logo",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"sort", "invalid sort value"},
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

func TestUpdateTeamHandler(t *testing.T) {
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
			name:     "partial update keeps other fields, bumps version",
			url:      "/v1/teams/1",
			body:     `{"name":"Thrylos"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Thrylos"`, `"version": 2`, `"logo": "https://x.example/oly.png"`, `"description": "Piraeus"`},
		},
		{
			name:     "full update",
			url:      "/v1/teams/1",
			body:     `{"name":"Thrylos","logo":"http://example.com/thrylos.png","description":"Rebuilt"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"name": "Thrylos"`, `"version": 2`, `"description": "Rebuilt"`},
		},
		{
			name:     "matching X-Expected-Version is accepted",
			url:      "/v1/teams/1",
			headers:  map[string]string{"X-Expected-Version": "1"},
			body:     `{"name":"Thrylos"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"version": 2`},
		},
		{
			name:     "stale X-Expected-Version is a conflict",
			url:      "/v1/teams/1",
			headers:  map[string]string{"X-Expected-Version": "9"},
			body:     `{"name":"Thrylos"}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"edit conflict"},
		},
		{
			name:     "unknown team",
			url:      "/v1/teams/999",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/teams/0",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/teams/abc",
			body:     `{"name":"X"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "empty body",
			url:      "/v1/teams/1",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/teams/1",
			body:     `{"name":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/teams/1",
			body:     `{"city":"Piraeus"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "empty name rejected",
			url:      "/v1/teams/1",
			body:     `{"name":""}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"name", "must be provided"},
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

// TestUpdateTeamHandlerPersists checks the row in the database after a PATCH,
// which the response-body assertions in TestUpdateTeamHandler cannot prove.
func TestUpdateTeamHandlerPersists(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPatch, "/v1/teams/1",
		strings.NewReader(`{"name":"Thrylos","description":"Rebuilt"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var name, logo, description string
	var version int
	err := testPool.QueryRow(context.Background(),
		`SELECT name, logo, description, version FROM teams WHERE id = 1`,
	).Scan(&name, &logo, &description, &version)
	if err != nil {
		t.Fatal(err)
	}

	if name != "Thrylos" || description != "Rebuilt" || version != 2 {
		t.Fatalf("row not persisted: name=%q description=%q version=%d", name, description, version)
	}
	if logo != "https://x.example/oly.png" {
		t.Fatalf("field not sent must be untouched, got logo=%q", logo)
	}
}

func TestDeleteTeamHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			// team 1 is referenced by canonical matches and a player
			// (players.favorite_team_id + matches RESTRICT) — must be a 409,
			// not a raw FK-violation 500.
			name:     "referenced team is a conflict",
			url:      "/v1/teams/1",
			wantCode: http.StatusConflict,
			wantBody: []string{"the record is referenced by other records and cannot be deleted"},
		},
		{
			name:     "unknown team",
			url:      "/v1/teams/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/teams/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/teams/abc",
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

// TestDeleteUnreferencedTeamHandler covers the happy path: a team with no
// matches or players can be deleted, and the row is really gone afterwards.
func TestDeleteUnreferencedTeamHandler(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/teams",
		strings.NewReader(`{"name":"Free FC","logo":"http://example.com/free.png","description":"no references"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	id := strings.TrimPrefix(rr.Header().Get("Location"), "/v1/teams/")

	req = httptest.NewRequest(http.MethodDelete, "/v1/teams/"+id, nil)
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("delete: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "successfully deleted") {
		t.Errorf("body missing confirmation (body: %s)", rr.Body.String())
	}

	var n int
	err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM teams WHERE id = $1`, id).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("team %s still in the database after delete", id)
	}
}
