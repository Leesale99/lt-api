package api

import (
	"context"
	"fmt"
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
			name: "existing round",
			// Round 3 is the canonical open round (ActionPhase).
			url:      "/v1/rounds/3",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 3`, `"status": "open"`},
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
			// Status is server-assigned on create (always "created"); the
			// client cannot set it, so it is an unknown key like any other.
			name:     "status in the body is rejected",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":3,"status":"open"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name: "valid round",
			// Season 1 hosts numbers 1-5; 6 is the first free one.
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":6}`,
			wantCode: http.StatusCreated,
			wantBody: []string{`"season_id": 1`, `"number": 6`, `"status": "created"`},
		},
		{
			name:     "unknown season",
			url:      "/v1/seasons/999/rounds",
			body:     `{"number":3}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "non-numeric season id",
			url:      "/v1/seasons/abc/rounds",
			body:     `{"number":3}`,
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
			body:     `{"number":3,"label":"derby"}`,
			wantCode: http.StatusBadRequest,
			wantBody: []string{"unknown key"},
		},
		{
			name:     "number zero",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":0}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
		},
		{
			name:     "number above league maximum",
			url:      "/v1/seasons/1/rounds",
			body:     `{"number":39}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"number"},
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

func TestUpdateRoundHandler(t *testing.T) {
	requireDB(t)

	tests := []struct {
		name     string
		url      string
		headers  map[string]string
		body     string
		wantCode int
		wantBody []string
		// setup plants state the lifecycle gates (000008) require for the
		// statement under test, e.g. settling a round's matches before a
		// close.
		setup func(t *testing.T)
	}{
		{
			name: "valid number update",
			// Round 4 is a created round; 6 is free in season 1.
			url:      "/v1/seasons/1/rounds/4",
			body:     `{"number":6}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 6`, `"version": 2`},
		},
		{
			name: "valid status update",
			// Round 3 is the canonical open round. rounds_close_gate (000008)
			// refuses a close over unsettled matches, so the fixture settles
			// them first: starts_at moves into the past (the score constraints
			// and the freeze gate key on it); the close transition stamps
			// ended_at. The rides side of the gate is empty in this fixture.
			setup: func(t *testing.T) {
				if _, err := testPool.Exec(context.Background(),
					`UPDATE matches SET starts_at = now() - interval '3 hours',
						status = 'closed', home_score = 2, away_score = 1
					 WHERE round_id = 3`); err != nil {
					t.Fatalf("settle round 3 matches: %v", err)
				}
			},
			url:      "/v1/seasons/1/rounds/3",
			body:     `{"status":"closed"}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"status": "closed"`, `"version": 2`},
		},
		{
			// Lowercase is the wire contract: vocabulary values are matched
			// exactly, there is no server-side normalization.
			name:     "uppercase status is rejected",
			url:      "/v1/seasons/1/rounds/3",
			body:     `{"status":"CLOSED"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "must be one of: created, open, closed"},
		},
		{
			name: "regressing the lifecycle is rejected",
			// Advisory check (rounds_freeze_gate backs it up in the DB):
			// round 1 is closed with started matches; regression is also
			// rank-rejected without them.
			url:      "/v1/seasons/1/rounds/1",
			body:     `{"status":"created"}`,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "earlier stage of the round lifecycle"},
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
			url:      "/v1/seasons/1/rounds/4",
			body:     `{}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 4`, `"version": 2`},
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
			name: "same number in another season is fine",
			// Round 6 is season 2's number-1 round; 3 is free there.
			url:      "/v1/seasons/2/rounds/6",
			body:     `{"number":3}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 3`},
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
			url:      "/v1/seasons/1/rounds/6",
			body:     `{"number":5}`,
			wantCode: http.StatusNotFound,
			wantBody: []string{"could not be found"},
		},
		{
			name:     "matching version header",
			// A number-only update on a created round: the lifecycle gates do
			// not fire (no status transition), the version machinery is what is
			// under test here.
			url:      "/v1/seasons/1/rounds/4",
			headers:  map[string]string{"X-Expected-Version": "1"},
			body:     `{"number":6}`,
			wantCode: http.StatusOK,
			wantBody: []string{`"version": 2`},
		},
		{
			name:     "stale version header",
			url:      "/v1/seasons/1/rounds/4",
			headers:  map[string]string{"X-Expected-Version": "9"},
			body:     `{"number":9}`,
			wantCode: http.StatusConflict,
			wantBody: []string{"edit conflict"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			app := newTestApplication()
			if tt.setup != nil {
				tt.setup(t)
			}

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

// TestUpdateRoundPersists asserts the update actually landed in the DB, not
// just in the response body.
func TestUpdateRoundPersists(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPatch, "/v1/seasons/1/rounds/4",
		// Number-only: round 4 has live matches, so closing it would hit
		// rounds_close_gate — the persistence check needs no status change.
		strings.NewReader(`{"number":7}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var number int
	var status string
	var version int
	err := testPool.QueryRow(context.Background(),
		`SELECT number, status, version FROM rounds WHERE id = 4`).Scan(&number, &status, &version)
	if err != nil {
		t.Fatal(err)
	}
	if number != 7 || status != "created" || version != 2 {
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
			// Round 1 is the canonical closed round (ADR-008).
			url:      "/v1/seasons/1/rounds/1",
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
		},
		{
			name: "open round with a started match is lifecycle-gated",
			// The fixture's open round has no started matches, so one is
			// seeded: the gate refuses the delete because it would cascade the
			// match away (ADR-008, same rule as the seasons delete gate).
			seed:     `INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at) VALUES (1, 3, 1, 2, 1.5, 2.5, 'created', now() - interval '1 hour')`,
			url:      "/v1/seasons/1/rounds/3",
			wantCode: http.StatusConflict,
			wantBody: []string{"referenced by other records"},
		},
		{
			name: "open round can be deleted",
			// Round 3 without the seeded started match is deletable.
			url:      "/v1/seasons/1/rounds/3",
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
			url:      "/v1/seasons/1/rounds/6",
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

// TestDeleteOpenRoundCascadesMatches covers the permitted path of the rounds
// delete gate: an open round whose matches have not started is hard-deleted
// and the cascade removes them (a started match would make the round durable
// — TestDeleteRoundHandler seeds one into round 3 for the gated case).
func TestDeleteOpenRoundCascadesMatches(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	// A match that has not started yet does not make the round durable.
	_, err := testPool.Exec(context.Background(),
		`INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES (1, 3, 1, 2, 1.5, 2.5, 'created', now() + interval '7 days')`)
	if err != nil {
		t.Fatalf("insert future match: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/seasons/1/rounds/3", nil)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var rounds, matches int
	err = testPool.QueryRow(context.Background(),
		`SELECT
			(SELECT count(*) FROM rounds WHERE id = 3),
			(SELECT count(*) FROM matches WHERE round_id = 3)`).Scan(&rounds, &matches)
	if err != nil {
		t.Fatal(err)
	}
	if rounds != 0 || matches != 0 {
		t.Fatalf("round 3 not fully deleted: %d round(s), %d match(es) remain", rounds, matches)
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
			wantBody: []string{`"total_records": 7`, `"number": 1`, `"number": 2`},
		},
		{
			name:     "filter by season_id",
			url:      "/v1/rounds?season_id=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 5`, `"season_id": 1`},
		},
		{
			name:     "filter by season_id no match",
			url:      "/v1/rounds?season_id=999",
			wantCode: http.StatusOK,
			// zero Metadata is omitzero-dropped, so an empty page carries no total_records
			wantBody: []string{`"rounds": []`},
		},
		{
			name:     "filter by status open",
			url:      "/v1/rounds?status=open",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`},
		},
		{
			name:     "filter by status closed",
			url:      "/v1/rounds?status=closed",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 2`},
		},
		{
			// Lowercase is the wire contract: vocabulary values are matched
			// exactly, there is no server-side normalization.
			name:     "uppercase status filter is rejected",
			url:      "/v1/rounds?status=OPEN",
			wantCode: http.StatusUnprocessableEntity,
			wantBody: []string{"status", "must be one of: created, open, closed"},
		},
		{
			name: "combined season_id and status",
			// Round 3 is season 1's only open round.
			url:      "/v1/rounds?season_id=1&status=open",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 1`, `"season_id": 1`, `"number": 3`},
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
			wantBody: []string{`"total_records": 7`},
		},
		{
			name:     "pagination honored",
			url:      "/v1/rounds?page=2&page_size=1",
			wantCode: http.StatusOK,
			wantBody: []string{`"total_records": 7`, `"current_page": 2`, `"number": 2`},
		},
		{
			name:     "sort by number ascending",
			url:      "/v1/rounds?page_size=1&sort=number",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 1`, `"total_records": 7`},
		},
		{
			name:     "sort by number descending",
			url:      "/v1/rounds?page_size=1&sort=-number",
			wantCode: http.StatusOK,
			wantBody: []string{`"number": 5`, `"total_records": 7`},
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

// TestCreateRoundDuplicateNumber covers the new ErrDuplicateRecord mapping on
// the create path: the second round with the same number in a season is a 409.
func TestCreateRoundDuplicateNumber(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons/1/rounds",
		strings.NewReader(`{"number":1}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))

	if rr.Code != http.StatusConflict {
		t.Fatalf("got status %d, want %d (body: %s)", rr.Code, http.StatusConflict, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unique values") {
		t.Errorf("body missing duplicate message (body: %s)", rr.Body.String())
	}
}

// TestOpenRoundStartsSeason covers ADR-008 point 3: flipping a round
// created → open flips its season open → in_progress in the same
// transaction. The fixture has no open season, so the test creates its own
// (a fresh season's status flips do not run into the advisory checks).
func TestOpenRoundStartsSeason(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	// An open season (create is always created; move it up via the update
	// transition created → open).
	req := httptest.NewRequest(http.MethodPost, "/v1/seasons", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create season: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	seasonID := strings.TrimPrefix(rr.Header().Get("Location"), "/v1/seasons/")

	req = httptest.NewRequest(http.MethodPatch, "/v1/seasons/"+seasonID,
		strings.NewReader(`{"status":"open"}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusOK {
		t.Fatalf("open season: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	// A created round under it.
	req = httptest.NewRequest(http.MethodPost, "/v1/seasons/"+seasonID+"/rounds",
		strings.NewReader(`{"number":1}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create round: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	roundID := strings.TrimPrefix(rr.Header().Get("Location"), fmt.Sprintf("/v1/seasons/%s/rounds/", seasonID))

	// Open the round: the season must flip in the same transaction.
	req = httptest.NewRequest(http.MethodPatch, "/v1/seasons/"+seasonID+"/rounds/"+roundID,
		strings.NewReader(`{"status":"open"}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusOK {
		t.Fatalf("open round: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var seasonStatus string
	var seasonVersion int
	err := testPool.QueryRow(context.Background(),
		`SELECT status, version FROM seasons WHERE id = $1`, seasonID).Scan(&seasonStatus, &seasonVersion)
	if err != nil {
		t.Fatal(err)
	}
	// Version history: 1 at create, 2 at the created → open PATCH in the
	// setup, 3 at the season flip riding the round open.
	if seasonStatus != "in_progress" || seasonVersion != 3 {
		t.Fatalf("season not flipped: status=%q version=%d, want in_progress/3", seasonStatus, seasonVersion)
	}
}

// TestOpenRoundSeasonFlipIsIdempotent asserts the season flip is conditional:
// an open round updated again (open → open no-op) does not touch the season
// a second time.
func TestOpenRoundSeasonFlipIsIdempotent(t *testing.T) {
	requireDB(t)

	reset(t)
	app := newTestApplication()

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create season: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	seasonID := strings.TrimPrefix(rr.Header().Get("Location"), "/v1/seasons/")

	req = httptest.NewRequest(http.MethodPatch, "/v1/seasons/"+seasonID,
		strings.NewReader(`{"status":"open"}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusOK {
		t.Fatalf("open season: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/seasons/"+seasonID+"/rounds",
		strings.NewReader(`{"number":1}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create round: got status %d, want %d (body: %s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	roundID := strings.TrimPrefix(rr.Header().Get("Location"), fmt.Sprintf("/v1/seasons/%s/rounds/", seasonID))

	// First PATCH: created → open, flips the season.
	req = httptest.NewRequest(http.MethodPatch, "/v1/seasons/"+seasonID+"/rounds/"+roundID,
		strings.NewReader(`{"status":"open"}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusOK {
		t.Fatalf("open round: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Second PATCH: no-op on the round, must not touch the season.
	req = httptest.NewRequest(http.MethodPatch, "/v1/seasons/"+seasonID+"/rounds/"+roundID,
		strings.NewReader(`{}`))
	rr = httptest.NewRecorder()
	app.routes().ServeHTTP(rr, withAuth(req, adminAuthToken))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-op round update: got status %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var seasonVersion int
	err := testPool.QueryRow(context.Background(),
		`SELECT version FROM seasons WHERE id = $1`, seasonID).Scan(&seasonVersion)
	if err != nil {
		t.Fatal(err)
	}
	// Versions: 1 at create, 2 at the created → open PATCH, 3 at the flip
	// riding the first round open; the second (no-op) PATCH must not add 4.
	if seasonVersion != 3 {
		t.Fatalf("season version bumped again: got %d, want 3", seasonVersion)
	}
}
