package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lt-api.aleksrdvn.com/internal/constants"
	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createMatchHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RoundID    int        `json:"round_id"`
		HomeTeamID int        `json:"home_team_id"`
		AwayTeamID int        `json:"away_team_id"`
		Status     string     `json:"status"`
		Odds       game.Odds  `json:"odds"`
		Score      game.Score `json:"score"`
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Store.Seasons.Get(ctx, seasonID); err != nil {
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
		Status:     strings.ToLower(input.Status),
		Odds:       input.Odds,
		Score:      input.Score,
	}

	v := validator.New()

	if game.ValidateMatch(v, match); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err := app.Store.Rounds.Get(ctx, input.RoundID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrRecordNotFound):
			v.AddError("round_id", "must reference an existing round")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}
	if round.SeasonID != seasonID {
		v.AddError("round_id", fmt.Sprintf("the round must belong to a season id %d", seasonID))
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if _, err := app.Store.Teams.Get(ctx, input.HomeTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrRecordNotFound):
			v.AddError("home_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	if _, err := app.Store.Teams.Get(ctx, input.AwayTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrRecordNotFound):
			v.AddError("away_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	match, err = app.Store.Matches.Insert(ctx, match)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			app.serverErrorResponse(w, r, err)
			return
		}
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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	match, err := app.Store.Matches.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
