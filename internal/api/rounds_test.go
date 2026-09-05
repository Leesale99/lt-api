package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowRoundHandler(t *testing.T) {
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
