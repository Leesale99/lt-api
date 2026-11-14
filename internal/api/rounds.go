package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/constants"
	schedule "lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
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

	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Schedule.Seasons.Get(ctx, seasonID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	round := schedule.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   schedule.RoundCreated, // server-assigned, see input struct
	}

	v := validator.New()

	if schedule.ValidateNewRound(v, round); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	round, err = app.Schedule.Rounds.Insert(ctx, round)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
			return
		default:
			httpx.ServerError(w, r, app.Logger, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d/rounds/%d", round.SeasonID, round.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"round": round}, headers)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) showRoundHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	round, err := app.Schedule.Rounds.Get(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"round": round}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) listRoundsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	seasonID := httpx.ReadInt(qs, "season_id", 0, v)
	status := schedule.RoundStatus(httpx.ReadString(qs, "status", ""))

	if status != "" {
		schedule.ValidateRoundStatus(v, status)
	}

	sortSafelist := []string{"id", "number", "-id", "-number"}
	filters, ok := httpx.ReadListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	rounds, metadata, err := app.Schedule.Rounds.GetAll(ctx, seasonID, status, filters)
	httpx.WriteListResponse(w, r, app.Logger, "rounds", rounds, metadata, err)
}

func (app *Application) updateRoundHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	roundID, err := httpx.ReadIDParam(r, "round_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	round, err := app.Schedule.Rounds.Get(ctx, roundID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	if round.SeasonID != seasonID {
		httpx.NotFound(w, r)
		return
	}

	current := round

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(round.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	// season_id is not part of the input: it is immutable after creation
	// (see RoundStore.Update for the composite-FK reason).
	var input struct {
		Number *int                  `json:"number"`
		Status *schedule.RoundStatus `json:"status"`
	}

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
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
		httpx.FailedValidation(w, r, v.Errors)
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
		case errors.Is(err, persistence.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, persistence.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
		case errors.Is(err, persistence.ErrRecordInUse):
			// A DB gate refused: rounds_freeze_gate (the update tried to
			// regress a round with started matches, ADR-008) or
			// rounds_close_gate (close over unsettled matches, ADR-022).
			// (The unresolved-rides half of the gate was relaxed in 000014 —
			// ride settlement is eventual now, behind RoundCompleted.)
			app.recordFrozenResponse(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"round": round}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) deleteRoundHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	roundID, err := httpx.ReadIDParam(r, "round_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	err = app.Schedule.Rounds.Delete(ctx, roundID, seasonID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordInUse):
			app.recordInUseResponse(w, r)
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "round successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}
