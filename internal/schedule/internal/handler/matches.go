package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

// createMatch is schedule's route (ADR-027): match creation is
// calendar bookkeeping — shape validation plus season/round/team existence
// checks — with no ride rules involved. The resolve PATCH below is the one
// match operation with a rule of its own (time policy), and it is
// schedule's script since the Game/Schedule split.
func (h *Handler) createMatch(w http.ResponseWriter, r *http.Request) {
	var input createMatchRequest

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

	match := model.Match{
		SeasonID:   seasonID,
		RoundID:    input.RoundID,
		HomeTeamID: input.HomeTeamID,
		AwayTeamID: input.AwayTeamID,
		Status:     model.MatchCreated, // server-assigned, see the request DTO
		StartsAt:   input.StartsAt,
		Odds:       oddsToModel(input.Odds),
		Score:      scoreToModel(input.Score),
	}

	v := validator.New()

	if model.ValidateNewMatch(v, match, time.Now()); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	round, err := h.svc.Rounds.Get(ctx, input.RoundID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			v.AddError("round_id", "must reference an existing round")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}
	if round.SeasonID != seasonID {
		v.AddError("round_id", fmt.Sprintf("the round must belong to a season id %d", seasonID))
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	if _, err := h.svc.Teams.Get(ctx, input.HomeTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			v.AddError("home_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	if _, err := h.svc.Teams.Get(ctx, input.AwayTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			v.AddError("away_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	match, err = h.svc.Matches.Insert(ctx, match)
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
	headers.Set("Location", fmt.Sprintf("/v1/matches/%d", match.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"match": matchFromModel(match)}, headers)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) showMatch(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	match, err := h.svc.Matches.Get(ctx, id)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"match": matchFromModel(match)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) listMatches(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	seasonID := httpx.ReadInt(qs, "season_id", 0, v)
	roundID := httpx.ReadInt(qs, "round_id", 0, v)
	status := model.MatchStatus(httpx.ReadString(qs, "status", ""))

	if status != "" {
		model.ValidateMatchStatus(v, status)
	}

	sortSafelist := []string{"id", "starts_at", "-id", "-starts_at"}
	filters, ok := httpx.ReadListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	matches, metadata, err := h.svc.Matches.GetAll(ctx, seasonID, roundID, status, filters)
	httpx.WriteListResponse(w, r, h.log, "matches", matchesFromModel(matches), metadata, err)
}

// deleteMatch runs the delete policy (service.DeleteMatch): the "match still
// has rides" question is the game context's, answered before the delete.
func (h *Handler) deleteMatch(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	matchID, err := httpx.ReadIDParam(r, "match_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	err = h.svc.DeleteMatch(ctx, matchID, seasonID)
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

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"message": "match successfully deleted"}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

func (h *Handler) updateMatch(w http.ResponseWriter, r *http.Request) {
	seasonID, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	matchID, err := httpx.ReadIDParam(r, "match_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	match, err := h.svc.Matches.Get(ctx, matchID)
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

	if match.SeasonID != seasonID {
		httpx.NotFound(w, r)
		return
	}

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(match.Version) != expectedVersion {
		httpx.EditConflict(w, r)
		return
	}

	current := match

	var input updateMatchRequest

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	if input.StartsAt != nil {
		match.StartsAt = *input.StartsAt
	}
	if input.HomeTeamID != nil {
		match.HomeTeamID = *input.HomeTeamID
	}
	if input.AwayTeamID != nil {
		match.AwayTeamID = *input.AwayTeamID
	}
	if input.Status != nil {
		match.Status = *input.Status
	}
	if input.Odds != nil {
		match.Odds = oddsToModel(*input.Odds)
	}
	if input.Score != nil {
		match.Score = scoreToModel(*input.Score)
	}

	v := validator.New()

	if model.ValidateMatchUpdate(v, current, match); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	if _, err := h.svc.Teams.Get(ctx, match.HomeTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			v.AddError("home_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	if _, err := h.svc.Teams.Get(ctx, match.AwayTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrRecordNotFound):
			v.AddError("away_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	match, err = h.svc.Matches.Update(ctx, match)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		case errors.Is(err, model.ErrRecordInUse):
			// matches_freeze_gate: the match has started and the update tried
			// to regress it (ADR-008).
			h.recordFrozenResponse(w, r)
		case errors.Is(err, model.ErrRecordNotFound):
			// Race: a referenced team was deleted between the checks above
			// and the update.
			v.AddError("home_team_id", "must reference an existing team")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"match": matchFromModel(match)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

// resolveMatch is schedule's route (Game/Schedule split): the resolve script
// commits the result in schedule's own transaction and publishes
// MatchResultRecorded post-commit — game settles the rides from the event
// payload, not in this request's transaction. The match types are
// schedule's (the row belongs to the calendar); the ride half lives behind
// the event seam.
func (h *Handler) resolveMatch(w http.ResponseWriter, r *http.Request) {
	matchID, err := httpx.ReadIDParam(r, "match_id")
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	var input resolveMatchRequest

	err = httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	match := model.Match{
		ID:      matchID,
		EndedAt: input.EndedAt,
		Score:   scoreToModel(input.Score),
		Version: input.Version,
	}

	v := validator.New()

	// Request shape only (ADR-026): the domain-legality rule (ended_at in
	// the past) runs inside the resolve use case, mapped to the same 422
	// below.
	if model.ValidateResolveMatch(v, match); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	match, err = h.svc.ResolveMatch(ctx, match.ID, match.Score, *match.EndedAt, match.Version)
	if err != nil {
		if errors.Is(err, model.ErrEndedAtInFuture) {
			// Time policy refusal from the service door (ADR-026), checked
			// before any transaction; the 422 body is the message this
			// handler used to produce itself.
			v.AddError("ended_at", "must be in the past")
			httpx.FailedValidation(w, r, v.Errors)
			return
		}
		// The resolve surface: ErrRecordNotFound (unknown match),
		// ErrEditConflict (lost race or already-closed match, classified by
		// the repository), canceled context. (The ride-half refusals left
		// this surface with the split: settlement is post-commit, behind the
		// event seam.)
		switch {
		case errors.Is(err, context.Canceled):
			// Client gone; response would be discarded anyway.
		case errors.Is(err, model.ErrRecordNotFound):
			httpx.NotFound(w, r)
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		default:
			httpx.ServerError(w, r, h.log, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"match": matchFromModel(match)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}
