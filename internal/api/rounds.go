package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/constants"
	schedule "lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createRoundHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Number int `json:"number"`
		// status is deliberately absent: new rounds are always created, the
		// client cannot set it (see ValidateNewRound). Opening a round is a
		// PATCH that must also start the season (RoundStore.Open), so creating
		// directly as open would bypass that transaction.
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Schedule.Seasons.Get(ctx, seasonID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
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

	round := schedule.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   schedule.RoundCreated, // server-assigned, see input struct
	}

	v := validator.New()

	if schedule.ValidateNewRound(v, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err = app.Schedule.Rounds.Insert(ctx, round)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrDuplicateRecord):
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

	round, err := app.Schedule.Rounds.Get(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
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
	status := schedule.RoundStatus(app.readString(qs, "status", ""))

	if status != "" {
		schedule.ValidateRoundStatus(v, status)
	}

	sortSafelist := []string{"id", "number", "-id", "-number"}
	filters, ok := app.readListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	rounds, metadata, err := app.Schedule.Rounds.GetAll(ctx, seasonID, status, filters)
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

	round, err := app.Schedule.Rounds.Get(ctx, roundID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
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
		Number *int                  `json:"number"`
		Status *schedule.RoundStatus `json:"status"`
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
		round.Status = *input.Status
	}

	v := validator.New()

	if schedule.ValidateRoundUpdate(v, current, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// ADR-008 point 3: the created → open transition also starts the season
	// (open → in_progress), and the two writes must succeed together — so it
	// goes through RoundStore.Open's transaction rather than Update.
	if current.Status == schedule.RoundCreated && round.Status == schedule.RoundOpen {
		round, err = app.Schedule.Rounds.Open(ctx, round)
	} else if current.Status == schedule.RoundOpen && round.Status == schedule.RoundClosed {
		// ADR-022 (event seam): closing flips the round (and the season, on the
		// last round) and publishes RoundCompleted post-commit — the game
		// context settles the undecided rides from the event payload
		// (auto-unlock rounds 1-37, auto-burn round 38). Ride settlement is
		// no longer this transaction's business, so the PATCH stays one tx
		// like the open transition.
		round, err = app.Schedule.Rounds.Close(ctx, round)
	} else {
		round, err = app.Schedule.Rounds.Update(ctx, round)
	}
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEditConflict):
			app.editConflictResponse(w, r)
		case errors.Is(err, store.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
		case errors.Is(err, store.ErrRecordInUse):
			// A DB gate refused: rounds_freeze_gate (the update tried to
			// regress a round with started matches, ADR-008) or
			// rounds_close_gate (close over unsettled matches, ADR-022).
			// (The unresolved-rides half of the gate was relaxed in 000014 —
			// ride settlement is eventual now, behind RoundCompleted.)
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

	err = app.Schedule.Rounds.Delete(ctx, roundID, seasonID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordInUse):
			app.recordInUseResponse(w, r)
		case errors.Is(err, store.ErrRecordNotFound):
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
