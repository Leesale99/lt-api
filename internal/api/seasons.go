package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"lt-api.aleksrdvn.com/internal/constants"
	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) listSeasonsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	id := app.readInt(qs, "id", 0, v)
	status := strings.ToLower(app.readString(qs, "status", ""))

	if status != "" {
		game.ValidateSeasonStatus(v, status)
	}

	filters, ok := app.readListFilters(w, r, qs, v, sortSafelistID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	seasons, metadata, err := app.Store.Seasons.GetAll(ctx, id, status, filters)
	app.writeListResponse(w, r, "seasons", seasons, metadata, err)
}

func (app *Application) createSeasonHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	season := game.Season{
		Status: strings.ToLower(input.Status),
	}

	v := validator.New()

	if game.ValidateSeason(v, season); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err = app.Store.Seasons.Insert(ctx, season)
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
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d", season.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"season": season}, headers)
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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err := app.Store.Seasons.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"season": season}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) updateSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err := app.Store.Seasons.Get(ctx, id)
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

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(season.Version) != expectedVersion {
		app.editConflictResponse(w, r)
		return
	}

	var input struct {
		Status *string `json:"status"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Status != nil {
		season.Status = strings.ToLower(*input.Status)
	}

	v := validator.New()

	if game.ValidateSeason(v, season); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	season, err = app.Store.Seasons.Update(ctx, season)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"season": season}, nil)
}

func (app *Application) deleteSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	err = app.Store.Seasons.Delete(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrRecordInUse):
			app.recordInUseResponse(w, r)
		case errors.Is(err, game.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"message": "season successfully deleted"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
