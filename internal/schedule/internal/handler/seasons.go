package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

func (h *Handler) listSeasons(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	id := httpx.ReadInt(qs, "id", 0, v)
	status := model.SeasonStatus(httpx.ReadString(qs, "status", ""))

	if status != "" {
		model.ValidateSeasonStatus(v, status)
	}

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	seasons, metadata, err := h.svc.Seasons.GetAll(ctx, id, status, filters)
	httpx.WriteListResponse(w, r, h.log, "seasons", seasonsFromModel(seasons), metadata, err)
}

func (h *Handler) createSeason(w http.ResponseWriter, r *http.Request) {
	var input createSeasonRequest

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	season := model.Season{
		Status: model.SeasonCreated, // server-assigned, see the request DTO
	}

	v := validator.New()

	if model.ValidateNewSeason(v, season); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	season, err = h.svc.Seasons.Insert(ctx, season)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			httpx.ServerError(w, r, h.log, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d", season.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"season": seasonFromModel(season)}, headers)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) showSeason(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	season, err := h.svc.Seasons.Get(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"season": seasonFromModel(season)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) updateSeason(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	season, err := h.svc.Seasons.Get(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	current := season

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(season.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	var input updateSeasonRequest

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	if input.Status != nil {
		season.Status = *input.Status
	}

	v := validator.New()

	if model.ValidateSeasonUpdate(v, current, season); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	season, err = h.svc.Seasons.Update(ctx, season)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, model.ErrRecordInUse):
			// seasons_freeze_gate: a match has started and the update tried
			// to move the season back to created/open (ADR-008).
			h.recordFrozenResponse(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"season": seasonFromModel(season)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

// deleteSeason runs the delete policy (service.DeleteSeason): the season's
// players go with it through the game context BEFORE the season row, the old
// cascade's shape and refusals preserved.
func (h *Handler) deleteSeason(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	err = h.svc.DeleteSeason(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordInUse):
			h.recordInUseResponse(w, r)
		case errors.Is(err, model.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "season successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}
