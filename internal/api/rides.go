package api

import (
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

// createRideHandler
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
		State:        game.RideLocked,
		TokensLocked: decimal.NewFromFloat(input.TokensLocked),
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
// lockRideHandler
// burnRideHandler
// unlockRideHandler
