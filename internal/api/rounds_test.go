package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowRoundHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "existing round",
			url:      "/v1/rounds/2",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 2`, `"status": "open"`},
		},
		{
			name:     "unknown round",
			url:      "/v1/rounds/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/rounds/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/rounds/abc",
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

func TestCreateRoundHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		body     string
		wantCode int
		wantBody []string
	}{
		{
			name:     "valid round",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":3,"status":"open"}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 1`, `"number": 3`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999/rounds",
			body:     `{"number":3,"status":"open"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric season id",
			url:      "/v1/seasons/abc/rounds",
			body:     `{"number":3,"status":"open"}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/1/rounds",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":3,"status":"open","label":"derby"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "number zero",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":0,"status":"open"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
		},
		{
			name:     "number above league maximum",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":39,"status":"open"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
		},
		{
			name:     "unknown status",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":3,"status":"pending"}`,
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

func TestUpdateRoundHandler(t *testing.T) {
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
			name:     "valid number update",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":5}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 5`, `"version": 2`},
		},
		{
			name:     "valid status update",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"status":"closed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "closed"`, `"version": 2`},
		},
		{
			name:     "uppercase status is normalized",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"status":"CLOSED"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "closed"`},
		},
		{
			name:     "empty body",
			url:      "/v1/seasons/1/rounds/2",
			body:     ``,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"body must not be empty"},
		},
		{
			name:     "badly-formed JSON",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"badly-formed JSON"},
		},
		{
			name:     "unknown field rejected",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":5,"season_id":2}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "empty object is a no-op",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 2`, `"version": 2`},
		},
		{
			name:     "number zero",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":0}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
		},
		{
			name:     "number above league maximum",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":39}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
		},
		{
			name:     "unknown status",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"status":"pending"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "duplicate number within season",
			url:      "/v1/seasons/1/rounds/2",
			body:     `{"number":1}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"unique values"},
		},
		{
			name:     "same number in another season is fine",
			url:      "/v1/seasons/2/rounds/3",
			body:     `{"number":2}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 2`},
		},
		{
			name:     "unknown round",
			url:      "/v1/seasons/1/rounds/999",
			body:     `{"number":5}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/1/rounds/0",
			body:     `{"number":5}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/1/rounds/abc",
			body:     `{"number":5}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "round in another season",
			url:      "/v1/seasons/1/rounds/3",
			body:     `{"number":5}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "matching version header",
			url:      "/v1/seasons/1/rounds/2",
			headers:  map[string]string{"X-Expected-Version": "1"},
			body:     `{"status":"closed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"version": 2`},
		},
		{
			name:     "stale version header",
			url:      "/v1/seasons/1/rounds/2",
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

// TestUpdateRoundPersists asserts the update actually landed in the DB, not
// just in the response body.
func TestUpdateRoundPersists(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPatch, "/v1/seasons/1/rounds/2",
		strings.NewReader(`{"number":7,"status":"closed"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var number int
	var status string
	var version int
	err := testPool.QueryRow(context.Background(),
		`SELECT number, status, version FROM rounds WHERE id = 2`).Scan(&number, &status, &version)
	if err != nil {
		t.Fatal(err)
	}
	if number != 7 || status != "closed" || version != 2 {
		t.Fatalf("row not updated: number=%d status=%q version=%d", number, status, version)
	}
}

func TestDeleteRoundHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		seed     string
		wantCode int
		wantBody []string
	}{
		{
			name: "closed round is lifecycle-gated",
			url:  "/v1/seasons/1/rounds/4",
			// number 3 is free in season 1 (canonical round 3 lives in season 2),
			// so the (season_id, number) unique constraint does not fire
			seed:     `INSERT INTO rounds (season_id, number, status) VALUES (1, 3, 'closed')`,
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
		},
		{
			name:     "open round can be deleted",
			url:      "/v1/seasons/1/rounds/2",
			wantCode: http.StatusOK,
			wantBody: []string{"successfully deleted"},
		},
		{
			name:     "unknown round",
			url:      "/v1/seasons/1/rounds/999",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "zero id",
			url:      "/v1/seasons/1/rounds/0",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric id",
			url:      "/v1/seasons/1/rounds/abc",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "round in another season",
			url:      "/v1/seasons/1/rounds/3",
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
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

// TestDeleteOpenRoundCascadesMatches covers the permitted path of the rounds
// delete gate: an open round is hard-deleted and the cascade removes its
// matches (round 1 hosts the canonical matches).
func TestDeleteOpenRoundCascadesMatches(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodDelete, "/v1/seasons/1/rounds/1", nil)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var rounds, matches int
	err := testPool.QueryRow(context.Background(),
		`SELECT
			(SELECT count(*) FROM rounds WHERE id = 1),
			(SELECT count(*) FROM matches WHERE round_id = 1)`).Scan(&rounds, &matches)
	if err != nil {
		t.Fatal(err)
	}
	if rounds != 0 || matches != 0 {
		t.Fatalf("round 1 not fully deleted: %d round(s), %d match(es) remain", rounds, matches)
	}
}

func TestListRoundsHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantBody []string
	}{
		{
			name:     "no filters",
			url:      "/v1/rounds",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 3`, `"number": 1`, `"number": 2`},
		},
		{
			name:     "filter by season_id",
			url:      "/v1/rounds?season_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`, `"season_id": 1`},
		},
		{
			name:     "filter by season_id no match",
			url:      "/v1/rounds?season_id=999",
			wantCode: http.StatusOK,
			// zero Metadata is omitzero-dropped, so an empty page carries no total_records
			wantBody: []string{`"rounds": []`},
		},
		{
			name:     "filter by status",
			url:      "/v1/rounds?status=open",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 3`},
		},
		{
			name:     "filter by status no match",
			url:      "/v1/rounds?status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"rounds": []`},
		},
		{
			name:     "uppercase status filter is normalized",
			url:      "/v1/rounds?status=OPEN",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 3`},
		},
		{
			name:     "combined season_id and status",
			url:      "/v1/rounds?season_id=2&status=open",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"season_id": 2`, `"number": 1`},
		},
		{
			name:     "combined season_id and status with no match",
			url:      "/v1/rounds?season_id=2&status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"rounds": []`},
		},
		{
			name:     "season_id zero is no filter",
			url:      "/v1/rounds?season_id=0",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 3`},
		},
		{
			name:     "pagination honored",
			url:      "/v1/rounds?page=2&page_size=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 3`, `"current_page": 2`, `"number": 2`},
		},
		{
			name:     "sort by number ascending",
			url:      "/v1/rounds?page_size=1&sort=number",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 1`, `"total_records": 3`},
		},
		{
			name:     "sort by number descending",
			url:      "/v1/rounds?page_size=1&sort=-number",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 2`, `"total_records": 3`},
		},
		{
			name:     "invalid status filter",
			url:      "/v1/rounds?status=playoff",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status"},
		},
		{
			name:     "name sort rejected by safelist",
			url:      "/v1/rounds?sort=name",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"sort", "invalid sort value"},
		},
		{
			name:     "page zero",
			url:      "/v1/rounds?page=0",
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

// TestCreateRoundDuplicateNumber covers the new ErrDuplicateRecord mapping on
// the create path: the second round with the same number in a season is a 409.
func TestCreateRoundDuplicateNumber(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons/1/rounds",
		strings.NewReader(`{"number":1,"status":"open"}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusConflict, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unique values") {
		t.Errorf("body missing duplicate message (body: %s)", rr.Body.String())
	}
}
