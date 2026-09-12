package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowSeasonHandler(t *testing.T) {
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
