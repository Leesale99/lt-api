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

func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request) {
	var input createTeamRequest

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	team := model.Team{
		Name:        input.Name,
		Logo:        input.Logo,
		Description: input.Description,
	}

	v := validator.New()

	if model.ValidateTeam(v, team); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	team, err = h.svc.Teams.Insert(ctx, team)
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
	headers.Set("Location", fmt.Sprintf("/v1/teams/%d", team.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"team": teamFromModel(team)}, headers)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) showTeam(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	team, err := h.svc.Teams.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"team": teamFromModel(team)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) updateTeam(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	team, err := h.svc.Teams.Get(ctx, id)
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

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(team.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	var input updateTeamRequest

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	if input.Name != nil {
		team.Name = *input.Name
	}

	if input.Logo != nil {
		team.Logo = *input.Logo
	}

	if input.Description != nil {
		team.Description = *input.Description
	}

	v := validator.New()
	if model.ValidateTeam(v, team); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	team, err = h.svc.Teams.Update(ctx, team)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"team": teamFromModel(team)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

// deleteTeam runs the delete policy (service.DeleteTeam): the "team still
// referenced by the game context" question is answered before the delete.
func (h *Handler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	err = h.svc.DeleteTeam(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "team successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	name := httpx.ReadString(qs, "name", "")

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistNameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	teams, metadata, err := h.svc.Teams.GetAll(ctx, name, filters)
	httpx.WriteListResponse(w, r, h.log, "teams", teamsFromModel(teams), metadata, err)
}
