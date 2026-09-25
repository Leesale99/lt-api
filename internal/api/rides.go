package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createRideHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PlayerID     int     `json:"player_id"`
		TeamID       int     `json:"team_id"`
		MatchID      int     `json:"match_id"`
		TokensLocked float64 `json:"token_locked"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	// TODO: token := app.Ledger.Tokens.GetByTeamID(input.TeamID)
	token := struct {
		ID   int
		Base decimal.Decimal
	}{
		ID:   1,
		Base: decimal.NewFromFloat(1.95),
	}

	ride := game.Ride{
		PlayerID:     input.PlayerID,
		TeamID:       input.TeamID,
		MatchID:      input.MatchID,
		TokensLocked: decimal.NewFromFloat(input.TokensLocked),
		State:        game.RideLocked,
		BaseAtLock:   token.Base,
		Acc:          decimal.Zero,
		Streak:       0,
	}

	v := validator.New()

	if game.ValidateRide(v, ride); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ride, err = app.Game.Rides.Insert(ride)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/rides/%d", ride.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"ride": ride}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// showRideHandler
// listRidesHandler
// updateRideHandler
// deleteRideHandler
func (app *Application) lockRideHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	ride, err := app.Game.RideLock(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.Is(err, game.ErrRoundNotOpen):
			app.roundNotOpenResponse(w, r)
		case errors.Is(err, game.ErrNoNextMatch):
			app.noNextMatchResponse(w, r)
		case errors.Is(err, game.ErrInvalidRoundPhase):
			app.invalidPhaseResponse(w, r)
		case errors.Is(err, game.ErrInvalidTransition):
			app.invalidStateTransitionResponse(w, r, ride.State)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"ride": ride}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// burnRideHandler
// unlockRideHandler
