package api

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
			wantBody: []string{`"status": "closed"`},
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

func TestCreateSeasonHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid season",
			body:     `{"status":"in_progress"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"status": "in_progress"`},
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
			body:     `{"status":"created","year":2026}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "missing status",
			body:     `{}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "unknown status",
			body:     `{"status":"playoff"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
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
			name:     "uppercase status is normalized",
			url:      "/v1/seasons/2",
			body:     `{"status":"OPEN"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "open"`},
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
			wantBody: []string{`"status": "in_progress"`, `"version": 2`},
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

func TestCreateSeasonHandlerLocation(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons", strings.NewReader(`{"status":"created"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

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
			name:     "closed season is lifecycle-gated",
			url:      "/v1/seasons/1",
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
		},
		{
			name:     "in_progress season is lifecycle-gated",
			url:      "/v1/seasons/2",
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
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

// TestDeleteCreatedSeasonHandler covers the permitted path of ADR-007: a
// 'created' season can be hard-deleted, the delete cascades to its rounds,
// and the trigger does not interfere.
func TestDeleteCreatedSeasonHandler(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons",
		strings.NewReader(`{"status":"created"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	id := strings.TrimPrefix(rr.Header().Get("Location"), "/v1/seasons/")

	// A round under the new season proves the cascade fires on delete.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO rounds (season_id, number, status) VALUES ($1, 1, 'open')`, id); err != nil {
		t.Fatalf("seed round: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/seasons/"+id, nil)
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("delete: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "successfully deleted") {
		t.Errorf("body missing confirmation (body: %s)", rr.Body.String())
	}

	var seasons, rounds int
	err := testPool.QueryRow(context.Background(),
		`SELECT
			(SELECT count(*) FROM seasons WHERE id = $1),
			(SELECT count(*) FROM rounds WHERE season_id = $1)`, id).Scan(&seasons, &rounds)
	if err != nil {
		t.Fatal(err)
	}
	if seasons != 0 || rounds != 0 {
		t.Fatalf("season %s not fully deleted: %d season(s), %d round(s) remain", id, seasons, rounds)
	}
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
			wantBody: []string{`"total_records": 2`, `"status": "closed"`, `"status": "in_progress"`},
		},
		{
			name:     "filter by id",
			url:      "/v1/seasons?id=2",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "in_progress"`, `"id": 2`},
		},
		{
			name:     "filter by status",
			url:      "/v1/seasons?status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "closed"`},
		},
		{
			name:     "uppercase status filter is normalized",
			url:      "/v1/seasons?status=IN_PROGRESS",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "in_progress"`},
		},
		{
			name:     "combined id and status",
			url:      "/v1/seasons?id=1&status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"status": "closed"`},
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
			wantBody: []string{`"total_records": 2`, `"status": "in_progress"`, `"current_page": 2`},
		},
		{
			name:     "descending sort",
			url:      "/v1/seasons?sort=-id",
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "in_progress"`},
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
