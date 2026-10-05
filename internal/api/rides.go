package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

// gameErrorResponse maps domain errors from the game service to HTTP
// responses. It returns true if it wrote a response.
func (app *Application) gameErrorResponse(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, context.Canceled):
		// Client gone; response would be discarded anyway.
	case errors.Is(err, store.ErrRecordNotFound):
		app.notFoundResponse(w, r)
	case errors.Is(err, game.ErrRoundNotOpen):
		app.roundNotOpenResponse(w, r)
	case errors.Is(err, game.ErrNoNextMatch):
		app.noNextMatchResponse(w, r)
	case errors.Is(err, game.ErrInvalidRoundPhase):
		app.invalidPhaseResponse(w, r)
	case errors.Is(err, game.ErrInvalidTransition):
		app.invalidStateTransitionResponse(w, r)
	case errors.Is(err, game.ErrIdempotencyConflict):
		app.idempotencyConflictResponse(w, r)
	default:
		app.serverErrorResponse(w, r, err)
	}
	return true
}

func (app *Application) createRideHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PlayerID     int   `json:"player_id"`
		TeamID       int   `json:"team_id"`
		MatchID      int   `json:"match_id"`
		TokensLocked int64 `json:"tokens_locked"`
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
		TokensLocked: decimal.NewFromInt(input.TokensLocked),
		BaseAtLock:   token.Base,
	}

	v := validator.New()

	if game.ValidateRide(v, ride); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	ride, err = app.Game.RideCreate(ctx, ride)
	if err != nil {
		app.gameErrorResponse(w, r, err)
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/rides/%d", ride.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"ride": ride}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showRideHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	ride, err := app.Game.Store.Rides.Get(ctx, id)
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

	err = app.writeJSON(w, http.StatusOK, envelope{"ride": ride}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) listRidesHandler(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()

	id := app.readInt(qs, "id", 0, v)
	playerID := app.readInt(qs, "player_id", 0, v)
	teamID := app.readInt(qs, "team_id", 0, v)
	matchID := app.readInt(qs, "match_id", 0, v)
	state := game.RideState(app.readString(qs, "state", ""))

	if state != "" {
		game.ValidateRideState(v, state)
	}

	sortSafeList := []string{
		"id",
		"player_id",
		"team_id",
		"match_id",
		"state",
		"-id",
		"-player_id",
		"-team_id",
		"-match_id",
		"-state",
	}

	filters, ok := app.readListFilters(w, r, qs, v, sortSafeList)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	rides, metadata, err := app.Game.Store.Rides.GetAll(ctx, id, playerID, teamID, matchID, state, filters)
	app.writeListResponse(w, r, "rides", rides, metadata, err)
}

// rideMarshal is the presentation callback the ride command handlers share:
// the same shape writeJSON emits (MarshalIndent + newline), so a replay is
// byte-identical to the original response (ADR-024).
func rideMarshal(ride game.Ride) (int, []byte, error) {
	body, err := json.MarshalIndent(envelope{"ride": ride}, "", "\t")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, append(body, '\n'), nil
}

// rideCommand is one ride command service call: ride id, idempotency token
// and the presentation callback in, the stored-or-fresh response out.
type rideCommand func(ctx context.Context, id int, token game.IdempotencyToken, marshal game.RideMarshal) (game.IdempotentResponse, error)

// rideCommandToken builds the command's dedup identity (ADR-024). The key
// rules live next to the model (game.ValidateIdempotencyToken); the hash
// covers method + request URI, so the same key on a different ride (or any
// altered request) is the 409 idempotency conflict. ok=false means the
// header failed validation and the 422 is written.
func (app *Application) rideCommandToken(w http.ResponseWriter, r *http.Request, endpoint string) (game.IdempotencyToken, bool) {
	key := r.Header.Get("Idempotency-Key")

	// The ride commands have no body; when a body-carrying command adopts
	// keys, its bytes join the hash input.
	hash := sha256.Sum256([]byte(r.Method + "\n" + r.URL.RequestURI()))

	token := game.IdempotencyToken{Key: key, Endpoint: endpoint, Hash: hash[:]}

	v := validator.New()

	if game.ValidateIdempotencyToken(v, token); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return game.IdempotencyToken{}, false
	}

	return token, true
}

// rideCommandHandler is the shared body of the three ride command endpoints
// (lock, burn, unlock): every ride command is idempotent (ADR-024), so each
// runs under an Idempotency-Key and a retry of an executed command replays
// the stored response.
func (app *Application) rideCommandHandler(endpoint string, command rideCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := app.readIDParam(r)
		if err != nil {
			app.notFoundResponse(w, r)
			return
		}

		token, ok := app.rideCommandToken(w, r, endpoint)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
		defer cancel()

		res, err := command(ctx, id, token, rideMarshal)
		if err != nil {
			app.gameErrorResponse(w, r, err)
			return
		}

		app.writeRawJSON(w, res.Status, res.Body)
	}
}
