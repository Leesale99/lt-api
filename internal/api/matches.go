package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lt-api.aleksrdvn.com/internal/constants"
	game "lt-api.aleksrdvn.com/internal/game"
	schedule "lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

// createMatchHandler is schedule's route (ADR-027): match creation is
// calendar bookkeeping — shape validation plus season/round/team existence
// checks — with no ride rules involved. MatchResolve (the one match
// operation that touches game state) is the deliberate exception: it stays
// wired to the game service below.
func (app *Application) createMatchHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RoundID    int            `json:"round_id"`
		HomeTeamID int            `json:"home_team_id"`
		AwayTeamID int            `json:"away_team_id"`
		StartsAt   time.Time      `json:"starts_at"`
		Odds       schedule.Odds  `json:"odds"`
		Score      schedule.Score `json:"score"`
		// status is deliberately absent: new matches are always created, the
		// client cannot set it (see ValidateNewMatch). Update owns the rest of
		// the lifecycle.
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	if _, err := app.Schedule.Seasons.Get(ctx, seasonID); err != nil {
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

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	match := schedule.Match{
		SeasonID:   seasonID,
		RoundID:    input.RoundID,
		HomeTeamID: input.HomeTeamID,
		AwayTeamID: input.AwayTeamID,
		Status:     schedule.MatchCreated, // server-assigned, see input struct
		StartsAt:   input.StartsAt,
		Odds:       input.Odds,
		Score:      input.Score,
	}

	v := validator.New()

	if schedule.ValidateNewMatch(v, match, time.Now()); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err := app.Schedule.Rounds.Get(ctx, input.RoundID)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			v.AddError("round_id", "must reference an existing round")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}
	if round.SeasonID != seasonID {
		v.AddError("round_id", fmt.Sprintf("the round must belong to a season id %d", seasonID))
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if _, err := app.Schedule.Teams.Get(ctx, input.HomeTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			v.AddError("home_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	if _, err := app.Schedule.Teams.Get(ctx, input.AwayTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			v.AddError("away_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	match, err = app.Schedule.Matches.Insert(ctx, match)
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
	headers.Set("Location", fmt.Sprintf("/v1/matches/%d", match.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"match": match}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showMatchHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	match, err := app.Schedule.Matches.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) listMatchHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()
	seasonID := app.readInt(qs, "season_id", 0, v)
	roundID := app.readInt(qs, "round_id", 0, v)
	status := schedule.MatchStatus(app.readString(qs, "status", ""))

	if status != "" {
		schedule.ValidateMatchStatus(v, status)
	}

	sortSafelist := []string{"id", "starts_at", "-id", "-starts_at"}
	filters, ok := app.readListFilters(w, r, qs, v, sortSafelist)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	matches, metadata, err := app.Schedule.Matches.GetAll(ctx, seasonID, roundID, status, filters)
	app.writeListResponse(w, r, "matches", matches, metadata, err)
}

func (app *Application) deleteMatchHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	matchID, err := app.readIDParam(r, "match_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	err = app.Schedule.Matches.Delete(ctx, matchID, seasonID)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"message": "match successfully deleted"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) updateMatchHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	matchID, err := app.readIDParam(r, "match_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	match, err := app.Schedule.Matches.Get(ctx, matchID)
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

	if match.SeasonID != seasonID {
		app.notFoundResponse(w, r)
		return
	}

	expectedVersion := r.Header.Get("X-Expected-Version")
	if expectedVersion != "" && strconv.Itoa(match.Version) != expectedVersion {
		app.editConflictResponse(w, r)
		return
	}

	current := match

	var input struct {
		StartsAt   *time.Time            `json:"starts_at"`
		HomeTeamID *int                  `json:"home_team_id"`
		AwayTeamID *int                  `json:"away_team_id"`
		Status     *schedule.MatchStatus `json:"status"`
		Odds       *schedule.Odds        `json:"odds"`
		Score      *schedule.Score       `json:"score"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
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
		match.Odds = *input.Odds
	}
	if input.Score != nil {
		match.Score = *input.Score
	}

	v := validator.New()

	if schedule.ValidateMatchUpdate(v, current, match); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if _, err := app.Schedule.Teams.Get(ctx, match.HomeTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			v.AddError("home_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	if _, err := app.Schedule.Teams.Get(ctx, match.AwayTeamID); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			v.AddError("away_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	match, err = app.Schedule.Matches.Update(ctx, match)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrEditConflict):
			app.editConflictResponse(w, r)
		case errors.Is(err, store.ErrRecordInUse):
			// matches_freeze_gate: the match has started and the update tried
			// to regress it (ADR-008).
			app.recordFrozenResponse(w, r)
		case errors.Is(err, store.ErrRecordNotFound):
			// Race: a referenced team was deleted between the checks above
			// and the update.
			v.AddError("home_team_id", "must reference an existing team")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// resolveMatchHandler is still game's route: MatchResolve must remain a
// single transactional operation (phase-03) and moving the entry to
// schedule now would force a schedule → game import. The match types are
// schedule's (the row belongs to the calendar); the orchestration is
// game's.
//
// TODO(Phase C): move resolve entry to schedule; split ride-outcome at the
// MatchResultRecorded event seam.
func (app *Application) resolveMatchHandler(w http.ResponseWriter, r *http.Request) {
	matchID, err := app.readIDParam(r, "match_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input struct {
		EndedAt *time.Time     `json:"ended_at"`
		Score   schedule.Score `json:"score"`
		Version int            `json:"version"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	match := schedule.Match{
		ID:      matchID,
		EndedAt: input.EndedAt,
		Score:   input.Score,
		Version: input.Version,
	}

	v := validator.New()

	// Request shape only (ADR-026): the domain-legality rule (ended_at in
	// the past) runs inside Service.MatchResolve, mapped to the same 422
	// below.
	if schedule.ValidateResolveMatch(v, match); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	match, err = app.Game.MatchResolve(ctx, match.ID, match.Score, *match.EndedAt, match.Version)
	if err != nil {
		if errors.Is(err, game.ErrEndedAtInFuture) {
			// Time policy refusal from the service door (ADR-026), checked
			// before any transaction; the 422 body is the message the
			// handler used to produce itself.
			v.AddError("ended_at", "must be in the past")
			app.failedValidationResponse(w, r, v.Errors)
			return
		}
		// gameErrorResponse maps the full domain surface this flow can
		// produce: ErrRecordNotFound (unknown match), ErrEditConflict (lost
		// race or already-closed match, classified by the store),
		// ErrInvalidTransition (rides_state_gate result agreement), canceled
		// context.
		app.gameErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
