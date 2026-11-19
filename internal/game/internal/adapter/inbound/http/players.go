package http

// The player HTTP surface: registration in a season, reads, patch and delete.
// The use cases own the rules (ownership, references, version guard); the
// handlers parse the request, hand the authenticated principal in as the
// actor, and map the answer onto the frozen wire shape.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/service"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// UserDirectory is the identity read this transport performs before writing a
// player's user reference: the authenticated principal's user row must exist
// (the cross-context FK is gone, migration 000016). It is declared here, at
// the consumer, and satisfied by the identity module's facade.
type UserDirectory interface {
	User(ctx context.Context, id int) (identity.User, error)
}

// actorOf maps the request's authenticated principal and permission set onto
// the use cases' Actor. A request without a principal yields the zero actor
// (unauthenticated, no authority).
func actorOf(r *http.Request) service.Actor {
	actor := service.Actor{
		CanManageAny: httpx.GetPermissions(r).Include("players:write:any"),
	}
	if user, found := httpx.GetAuthenticatedUser(r); found {
		actor.Authenticated = true
		actor.UserID = user.ID
	}
	return actor
}

func (h *Handler) createPlayer(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name           string `json:"name"`
		FavoriteTeamID int    `json:"favorite_team_id"`
	}

	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
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

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	// User existence is a transport-level check since the cross-context FKs
	// came out (migration 000016 — identity is another context: the
	// players_user_id_fkey is gone). user_id is only ever the authenticated
	// principal's ID, so this is the one spot the check belongs. The refusal
	// is a field error on the reference that actually failed — the principal's
	// user account, not the favourite team (misalignment-report §6 item 12).
	if h.users != nil {
		if _, err := h.users.User(ctx, authenticatedUser.ID); err != nil {
			switch {
			case errors.Is(err, context.Canceled):
				return
			case errors.Is(err, identity.ErrNotFound):
				v := validator.New()
				v.AddError("user_id", "must reference an existing user account")
				httpx.FailedValidation(w, r, v.Errors)
			default:
				httpx.ServerError(w, r, h.log, err)
			}
			return
		}
	}

	player := domain.Player{
		SeasonID:       seasonID,
		FavoriteTeamID: input.FavoriteTeamID,
		Name:           input.Name,
	}

	v := validator.New()

	if domain.ValidatePlayer(v, player); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	player, err = h.svc.PlayerCreate(ctx, actorOf(r), seasonID, player)
	if err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/players/%d", player.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"player": playerFromDomain(player)}, headers)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) showPlayer(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	player, err := h.svc.PlayerGet(ctx, id)
	if err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"player": playerFromDomain(player)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) updatePlayer(w http.ResponseWriter, r *http.Request) {
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

	var input struct {
		Name           *string `json:"name"`
		FavoriteTeamID *int    `json:"favorite_team_id"`
	}

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	// X-Expected-Version is the client's optimistic-concurrency claim; the
	// absence of the header means "no claim" (the write's own guard is then the
	// only one). A header that is present but unreadable cannot match any
	// stored version — the stale-claim conflict, which is what the pre-refactor
	// handler answered a malformed value with.
	var expectedVersion *int
	if raw := r.Header.Get("X-Expected-Version"); raw != "" {
		claimed, err := strconv.Atoi(raw)
		if err != nil {
			h.gameErrorResponse(w, r, domain.ErrEditConflict)
			return
		}
		expectedVersion = &claimed
	}

	patch := domain.PlayerPatch{
		Name:           input.Name,
		FavoriteTeamID: input.FavoriteTeamID,
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	player, err := h.svc.PlayerUpdate(ctx, actorOf(r), seasonID, playerID, patch, expectedVersion)
	if err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"player": playerFromDomain(player)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) deletePlayer(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	if err := h.svc.PlayerDelete(ctx, actorOf(r), seasonID, playerID); err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "player successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) listPlayers(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	name := httpx.ReadString(qs, "name", "")
	favoriteTeamID := httpx.ReadInt(qs, "favorite_team_id", 0, v)

	filters, ok := httpx.ReadListFilters(w, r, qs, v, httpx.SortSafelistNameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	players, metadata, err := h.svc.PlayerList(ctx, name, favoriteTeamID, filters)
	httpx.WriteListResponse(w, r, h.log, "players", playersFromDomain(players), metadata, err)
}
