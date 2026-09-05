package api

import (
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createPlayerHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FavouriteTeamID int `json:"favourite_team_id"`
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

	player := game.Player{
		SeasonID:        seasonID,
		FavouriteTeamID: input.FavouriteTeamID,
	}

	v := validator.New()

	if game.ValidatePlayer(v, player); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if _, err := app.Store.Teams.Get(player.FavouriteTeamID); err != nil {
		v.AddError("favourite_team_id", "must reference an existing team")
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	player, err = app.Store.Players.Insert(player)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"player": player}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showPlayerHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	player, err := app.Store.Players.Get(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"player": player}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
