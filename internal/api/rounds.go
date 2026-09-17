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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Game.Seasons.Get(ctx, seasonID); err != nil {
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

	round := game.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   strings.ToLower(input.Status),
	}

	v := validator.New()

	if game.ValidateRound(v, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err = app.Game.Rounds.Insert(ctx, round)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, game.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
			return
		default:
			app.serverErrorResponse(w, r, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d/rounds/%d", round.SeasonID, round.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"round": round}, headers)
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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	round, err := app.Game.Rounds.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) listRoundsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	seasonID := app.readInt(qs, "season_id", 0, v)
	status := strings.ToLower(app.readString(qs, "status", ""))

	if status != "" {
		game.ValidateRoundStatus(v, status)
	}

	sortSafelist := []string{"id", "number", "-id", "-number"}
	filters, ok := app.readListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	rounds, metadata, err := app.Game.Rounds.GetAll(ctx, seasonID, status, filters)
	app.writeListResponse(w, r, "rounds", rounds, metadata, err)
}

func (app *Application) updateRoundHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	roundID, err := app.readIDParam(r, "round_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	round, err := app.Game.Rounds.Get(ctx, roundID)
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

	if round.SeasonID != seasonID {
		app.notFoundResponse(w, r)
		return
	}

	current := round

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(round.Version) != expectedVersion {
		app.editConflictResponse(w, r)
		return
	}

	// season_id is not part of the input: it is immutable after creation
	// (see RoundStore.Update for the composite-FK reason).
	var input struct {
		Number *int    `json:"number"`
		Status *string `json:"status"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Number != nil {
		round.Number = *input.Number
	}
	if input.Status != nil {
		round.Status = strings.ToLower(*input.Status)
	}

	v := validator.New()

	if game.ValidateRoundUpdate(v, current, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// ADR-008 point 3: the created → open transition also starts the season
	// (open → in_progress), and the two writes must succeed together — so it
	// goes through RoundStore.Open's transaction rather than Update.
	if current.Status == "created" && round.Status == "open" {
		round, err = app.Game.Rounds.Open(ctx, round)
	} else {
		round, err = app.Game.Rounds.Update(ctx, round)
	}
	if err != nil {
		switch {
		case errors.Is(err, game.ErrEditConflict):
			app.editConflictResponse(w, r)
		case errors.Is(err, game.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
		case errors.Is(err, game.ErrRecordInUse):
			// rounds_freeze_gate: a match of this round has started and the
			// update tried to regress it (ADR-008).
			app.recordFrozenResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) deleteRoundHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	roundID, err := app.readIDParam(r, "round_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	err = app.Game.Rounds.Delete(ctx, roundID, seasonID)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"message": "round successfully deleted"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
