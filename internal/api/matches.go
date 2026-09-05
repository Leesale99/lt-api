package api

import (
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createMatchHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RoundID    int         `json:"round_id"`
		HomeTeamID int         `json:"home_team_id"`
		AwayTeamID int         `json:"away_team_id"`
		Status     string      `json:"status"`
		Odds       game.Odds   `json:"odds"`
		Score      *game.Score `json:"score"`
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

	match := game.Match{
		SeasonID:   seasonID,
		RoundID:    input.RoundID,
		HomeTeamID: input.HomeTeamID,
		AwayTeamID: input.AwayTeamID,
		Status:     input.Status,
		Odds:       input.Odds,
		Score:      input.Score,
	}

	v := validator.New()

	if game.ValidateMatch(v, match); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	match, err = app.Store.Matches.Insert(match)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showMatchHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	match, err := app.Store.Matches.Get(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
