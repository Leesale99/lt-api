package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/constants"
	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

// userCanManagePlayer reports whether the authenticated user may modify the
// given player: the owner may always manage their own registration; any
// other user only if their role carries players:write:any (admin). A player
// with a NULL user_id has no owner — admin-only by design.
func (app *Application) userCanManagePlayer(r *http.Request, player game.Player) bool {
	authenticatedUser, found := httpx.GetAuthenticatedUser(r)
	if found && player.UserID != nil && *player.UserID == authenticatedUser.ID {
		return true
	}
	return httpx.GetPermissions(r).Include("players:write:any")
}

func (app *Application) createPlayerHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name           string `json:"name"`
		FavoriteTeamID int    `json:"favorite_team_id"`
	}

	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Schedule.Season(ctx, seasonID); err != nil {
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

	authenticatedUser, found := httpx.GetAuthenticatedUser(r)
	if !found {
		httpx.AuthenticationRequired(w, r)
		return
	}

	// Player user existence is an api-level check since the cross-context
	// FKs came out (migration 000016 — identity is another context: the
	// players_user_id_fkey is gone), and api holds the identity store.
	// user_id is only ever the authenticated principal's ID (the
	// update path never writes it), so this is the one spot the check
	// belongs. The refusal keeps the shape the FK refusal produced:
	// persistence.ErrRecordNotFound → the 422 favorite_team_id validation error.
	if _, err := app.Identity.Users.Get(ctx, authenticatedUser.ID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			v := validator.New()
			v.AddError("favorite_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	player := game.Player{
		SeasonID:       seasonID,
		FavoriteTeamID: input.FavoriteTeamID,
		Name:           input.Name,
		UserID:         &authenticatedUser.ID,
	}

	v := validator.New()

	if game.ValidatePlayer(v, player); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	if _, err := app.Schedule.Team(ctx, player.FavoriteTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			v.AddError("favorite_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	player, err = app.Game.Store.Players.Insert(ctx, player)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrDuplicateRecord):
			app.duplicateRecordResponse(w, r)
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			// Race: the team was deleted between the check above and the insert.
			v.AddError("favorite_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, app.Logger, err)
			return
		}
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/players/%d", player.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"player": player}, headers)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) showPlayerHandler(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	player, err := app.Game.Store.Players.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"player": player}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) updatePlayerHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	playerID, err := httpx.ReadIDParam(r, "player_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	player, err := app.Game.Store.Players.Get(ctx, playerID)
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

	if player.SeasonID != seasonID {
		httpx.NotFound(w, r)
		return
	}

	if !app.userCanManagePlayer(r, player) {
		httpx.Forbidden(w, r)
		return
	}

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(player.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	var input struct {
		Name           *string `json:"name"`
		FavoriteTeamID *int    `json:"favorite_team_id"`
	}

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	if input.Name != nil {
		player.Name = *input.Name
	}
	if input.FavoriteTeamID != nil {
		player.FavoriteTeamID = *input.FavoriteTeamID
	}

	v := validator.New()

	if game.ValidatePlayer(v, player); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	if _, err := app.Schedule.Team(ctx, player.FavoriteTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			v.AddError("favorite_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	player, err = app.Game.Store.Players.Update(ctx, player)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, persistence.ErrRecordNotFound):
			// Race: the team was deleted between the check above and the update.
			v.AddError("favorite_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"player": player}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) deletePlayerHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	playerID, err := httpx.ReadIDParam(r, "player_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	player, err := app.Game.Store.Players.Get(ctx, playerID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
			return
		default:
			httpx.ServerError(w, r, app.Logger, err)
			return
		}
	}

	if player.SeasonID != seasonID {
		httpx.NotFound(w, r)
		return
	}

	if !app.userCanManagePlayer(r, player) {
		httpx.Forbidden(w, r)
		return
	}

	err = app.Game.Store.Players.Delete(ctx, playerID, seasonID)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "player successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) listPlayersHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	name := httpx.ReadString(qs, "name", "")
	favoriteTeamID := httpx.ReadInt(qs, "favorite_team_id", 0, v)

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistNameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	players, metadata, err := app.Game.Store.Players.GetAll(ctx, name, favoriteTeamID, filters)
	httpx.WriteListResponse(w, r, app.Logger, "players", players, metadata, err)
}
