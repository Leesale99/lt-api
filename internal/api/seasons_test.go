package api

import (
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
