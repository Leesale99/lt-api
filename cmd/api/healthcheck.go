package main

// healthcheck.go is the liveness route's home: GET /v1/healthcheck, answering
// the same {"status":"available","environment":...,"version":...} envelope the
// pre-phase-6 gateway package answered. It needs the server's three process
// facts and nothing else.

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/platform/server"
)

func healthcheckHandler(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := httpx.Envelope{
			"status":      "available",
			"environment": srv.Env,
			"version":     srv.Version,
		}

		err := httpx.WriteJSON(w, http.StatusOK, data, nil)
		if err != nil {
			httpx.ServerError(w, r, srv.Logger, err)
		}
	}
}
