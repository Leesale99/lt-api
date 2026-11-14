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

func (app *Application) createTeamHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Logo        string `json:"logo"`
		Description string `json:"description"`
	}

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	team := schedule.Team{
		Name:        input.Name,
		Logo:        input.Logo,
		Description: input.Description,
	}

	v := validator.New()

	if schedule.ValidateTeam(v, team); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err = app.Schedule.Teams.Insert(ctx, team)
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
	headers.Set("Location", fmt.Sprintf("/v1/teams/%d", team.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"team": team}, headers)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) showTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err := app.Schedule.Teams.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"team": team}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) updateTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err := app.Schedule.Teams.Get(ctx, id)
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

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(team.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	var input struct {
		Name        *string `json:"name"`
		Logo        *string `json:"logo"`
		Description *string `json:"description"`
	}

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
	if schedule.ValidateTeam(v, team); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	team, err = app.Schedule.Teams.Update(ctx, team)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrEditConflict):
			httpx.EditConflict(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"team": team}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) deleteTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	// The players_favorite_team_id_fkey and rides_team_id_fkey
	// RESTRICTs are gone with the cross-context FKs (migration 000016), so
	// the "team still referenced by the game context" refusal is composed
	// here: api holds both stores, game answers its own references (matches
	// RESTRICT is schedule-internal and still enforced by the DB). Same
	// ErrRecordInUse → 409 surface the FK produced. The race — a player or
	// ride created between the check and the delete — is the accepted
	// api-level gap (the same window the delete-then-FK flow had between
	// its own read and write).
	playerRefs, err := app.Game.Store.Players.ExistsForTeam(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}
	rideRefs, err := app.Game.Store.Rides.ExistsForTeam(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}
	if playerRefs || rideRefs {
		app.recordInUseResponse(w, r)
		return
	}

	err = app.Schedule.Teams.Delete(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "team successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) listTeamsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	name := httpx.ReadString(qs, "name", "")

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistNameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	teams, metadata, err := app.Schedule.Teams.GetAll(ctx, name, filters)
	httpx.WriteListResponse(w, r, app.Logger, "teams", teams, metadata, err)
}
