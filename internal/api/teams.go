package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createTeamHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Logo        string `json:"logo"`
		Description string `json:"description"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	team := game.Team{
		Name:        input.Name,
		Logo:        input.Logo,
		Description: input.Description,
	}

	v := validator.New()

	if game.ValidateTeam(v, team); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err = app.Store.Teams.Insert(ctx, team)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			app.serverErrorResponse(w, r, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/teams/%d", team.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err := app.Store.Teams.Get(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
