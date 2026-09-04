package api

import (
	"net/http"
)

func (app *Application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data := envelope{
		"status":      "available",
		"environment": app.Env,
		"version":     app.Version,
	}

	err := app.writeJSON(w, http.StatusOK, data, nil)
	if err != nil {
		app.Logger.Error(err.Error())
		http.Error(w, "The server encountered a problem and could not process your request", http.StatusInternalServerError)
	}
}

func (app *Application) showSeasons(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var ids []int
	ids = append(ids, id)

	seasons, err := app.Game.GetSeasons(ids)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"seasons": seasons}, nil)
	if err != nil {
		app.Logger.Error(err.Error())
		http.Error(w, "The server encountered a problem and could not process your request", http.StatusInternalServerError)
	}
}
