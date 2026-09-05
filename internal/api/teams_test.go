package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowTeamHandler(t *testing.T) {
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

func TestCreateTeamHandler(t *testing.T) {
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
			app := newTestApplication()

			var reader io.Reader
			if tt.body != "" {
				reader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/teams", reader)
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
