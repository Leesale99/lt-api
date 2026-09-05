package api

import (
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createRoundHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Number int    `json:"number"`
		Status string `json:"status"`
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	if _, err := app.Store.Seasons.Get(seasonID); err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	round := game.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   input.Status,
	}

	v := validator.New()

	if game.ValidateRound(v, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err = app.Store.Rounds.Insert(round)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showRoundHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	round, err := app.Store.Rounds.Get(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
