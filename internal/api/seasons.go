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

func (app *Application) listSeasonsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	id := httpx.ReadInt(qs, "id", 0, v)
	status := schedule.SeasonStatus(httpx.ReadString(qs, "status", ""))

	if status != "" {
		schedule.ValidateSeasonStatus(v, status)
	}

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	seasons, metadata, err := app.Schedule.Seasons.GetAll(ctx, id, status, filters)
	httpx.WriteListResponse(w, r, app.Logger, "seasons", seasons, metadata, err)
}

func (app *Application) createSeasonHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// status is deliberately absent: new seasons are always created, the
		// client cannot set it (see ValidateNewSeason). Update owns the rest
		// of the lifecycle.
	}

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	season := schedule.Season{
		Status: schedule.SeasonCreated, // server-assigned, see input struct
	}

	v := validator.New()

	if schedule.ValidateNewSeason(v, season); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err = app.Schedule.Seasons.Insert(ctx, season)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			httpx.ServerError(w, r, app.Logger, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d", season.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"season": season}, headers)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) showSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err := app.Schedule.Seasons.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"season": season}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) updateSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	season, err := app.Schedule.Seasons.Get(ctx, id)
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

	current := season

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(season.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	var input struct {
		Status *schedule.SeasonStatus `json:"status"`
	}

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	if input.Status != nil {
		season.Status = *input.Status
	}

	v := validator.New()

	if schedule.ValidateSeasonUpdate(v, current, season); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	season, err = app.Schedule.Seasons.Update(ctx, season)
	if err != nil {
		switch {
		case errors.Is(err, persistence.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, persistence.ErrRecordInUse):
			// seasons_freeze_gate: a match has started and the update tried
			// to move the season back to created/open (ADR-008).
			app.recordFrozenResponse(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"season": season}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) deleteSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	// players_season_id_fkey's CASCADE is gone with the
	// cross-context FKs (migration 000016), so the api handler replays the
	// cascade's game half explicitly: the season's players are deleted
	// through game BEFORE the season. The row outcome is the old
	// cascade's, and the old refusals keep their surfaces: a ride still
	// referencing a player trips rides_player_id_fkey (a same-context FK)
	// → ErrRecordInUse → the 409 below, exactly what the blocked cascade
	// produced. The accepted gap: if the season delete itself then fails,
	// the players are already gone (the old flow deleted everything or
	// nothing in one statement). Only deleteable seasons (no started
	// matches — the seasons_delete_gate) reach the players step.
	if err := app.Game.Store.Players.DeleteForSeason(ctx, id); err != nil {
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

	err = app.Schedule.Seasons.Delete(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "season successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}
