package api

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/shared/httpx"
)

func (app *Application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data := httpx.Envelope{
		"status":      "available",
		"environment": app.Env,
		"version":     app.Version,
	}

	err := httpx.WriteJSON(w, http.StatusOK, data, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}
