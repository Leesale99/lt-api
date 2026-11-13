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

func (app *Application) createTeamHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Logo        string `json:"logo"`
		Description string `json:"description"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	team := schedule.Team{
		Name:        input.Name,
		Logo:        input.Logo,
		Description: input.Description,
	}

	v := validator.New()

	if schedule.ValidateTeam(v, team); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
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
			app.serverErrorResponse(w, r, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/teams/%d", team.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"team": team}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err := app.Schedule.Teams.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) updateTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	team, err := app.Schedule.Teams.Get(ctx, id)
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

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(team.Version) != expectedVersion {
		app.editConflictResponse(w, r)
		return
	}

	var input struct {
		Name        *string `json:"name"`
		Logo        *string `json:"logo"`
		Description *string `json:"description"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
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
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	team, err = app.Schedule.Teams.Update(ctx, team)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) deleteTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	// (Phase D) The players_favorite_team_id_fkey and rides_team_id_fkey
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
			app.serverErrorResponse(w, r, err)
		}
		return
	}
	rideRefs, err := app.Game.Store.Rides.ExistsForTeam(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		default:
			app.serverErrorResponse(w, r, err)
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
		case errors.Is(err, store.ErrRecordInUse):
			app.recordInUseResponse(w, r)
		case errors.Is(err, store.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"message": "team successfully deleted"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) listTeamsHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	name := app.readString(qs, "name", "")

	filters, ok := app.readListFilters(w, r, qs, v, sortSafelistNameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	teams, metadata, err := app.Schedule.Teams.GetAll(ctx, name, filters)
	app.writeListResponse(w, r, "teams", teams, metadata, err)
}
