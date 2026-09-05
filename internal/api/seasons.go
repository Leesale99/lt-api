package api

import (
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createSeasonHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		State string `json:"state"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	season := game.Season{
		State: input.State,
	}

	v := validator.New()

	if game.ValidateSeason(v, season); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	season, err = app.Store.Seasons.Insert(season)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"season": season}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	season, err := app.Store.Seasons.Get(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"season": season}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
