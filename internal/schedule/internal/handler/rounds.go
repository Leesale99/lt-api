package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (h *Handler) createRound(w http.ResponseWriter, r *http.Request) {
	var input createRoundRequest

	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	if _, err := h.svc.Seasons.Get(ctx, seasonID); err != nil {
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

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	round := model.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   model.RoundCreated, // server-assigned, see the request DTO
	}

	v := validator.New()

	if model.ValidateNewRound(v, round); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	round, err = h.svc.Rounds.Insert(ctx, round)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrDuplicateRecord):
			h.duplicateRecordResponse(w, r)
			return
		default:
			httpx.ServerError(w, r, h.log, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/seasons/%d/rounds/%d", round.SeasonID, round.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"round": roundFromModel(round)}, headers)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) showRound(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	round, err := h.svc.Rounds.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"round": roundFromModel(round)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) listRounds(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	seasonID := httpx.ReadInt(qs, "season_id", 0, v)
	status := model.RoundStatus(httpx.ReadString(qs, "status", ""))

	if status != "" {
		model.ValidateRoundStatus(v, status)
	}

	sortSafelist := []string{"id", "number", "-id", "-number"}
	filters, ok := httpx.ReadListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	rounds, metadata, err := h.svc.Rounds.GetAll(ctx, seasonID, status, filters)
	httpx.WriteListResponse(w, r, h.log, "rounds", roundsFromModel(rounds), metadata, err)
}

// updateRound keeps the request shape (IDs, the X-Expected-Version header,
// the decoded patch, the validators) and hands the lifecycle transition to
// the service: created → open and open → closed carry transactions and an
// event, plain edits are writes (service.RoundService.UpdateRound).
func (h *Handler) updateRound(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	round, err := h.svc.Rounds.Get(ctx, roundID)
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

	var input updateRoundRequest

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

	if model.ValidateRoundUpdate(v, current, round); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	round, err = h.svc.Rounds.UpdateRound(ctx, current, round, expectedVersion)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, model.ErrDuplicateRecord):
			h.duplicateRecordResponse(w, r)
		case errors.Is(err, model.ErrRecordInUse):
			// A DB gate refused: rounds_freeze_gate (the update tried to
			// regress a round with started matches, ADR-008) or
			// rounds_close_gate (close over unsettled matches, ADR-022).
			// (The unresolved-rides half of the gate was relaxed in 000014 —
			// ride settlement is eventual now, behind RoundCompleted.)
			h.recordFrozenResponse(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"round": roundFromModel(round)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) deleteRound(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	err = h.svc.Rounds.Delete(ctx, roundID, seasonID)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "round successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}
