package http

// The ride HTTP surface: two reads and the idempotent commands (create, lock,
// burn, unlock). Every command runs under an Idempotency-Key (ADR-024) and a
// retry of an executed command replays the stored response byte-identical —
// the Location header of a created ride included, derived from the stored
// body so a replay answers it too.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/game/internal/core/service"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/idempotency"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

func (h *Handler) showRide(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.ReadIDParam(r)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	ride, err := h.svc.RideGet(ctx, id)
	if err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"ride": rideFromDomain(ride)}, nil)
	if err != nil {
		httpx.ServerError(w, r, h.log, err)
	}
}

// rule:R-GAME-020
func (h *Handler) listRides(w http.ResponseWriter, r *http.Request) {
	v := validator.New()

	qs := r.URL.Query()

	id := httpx.ReadInt(qs, "id", 0, v)
	playerID := httpx.ReadInt(qs, "player_id", 0, v)
	teamID := httpx.ReadInt(qs, "team_id", 0, v)
	matchID := httpx.ReadInt(qs, "match_id", 0, v)
	state := domain.RideState(httpx.ReadString(qs, "state", ""))

	if state != "" {
		domain.ValidateRideState(v, state)
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

	filters, ok := httpx.ReadListFilters(w, r, qs, v, sortSafeList)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	rides, metadata, err := h.svc.RideList(ctx, id, playerID, teamID, matchID, state, filters)
	httpx.WriteListResponse(w, r, h.log, "rides", ridesFromDomain(rides), metadata, err)
}

// rideCommand is one ride command use case call: ride id, idempotency token
// and the presentation callback in, the stored-or-fresh response out.
type rideCommand func(ctx context.Context, id int, token port.Token, marshal service.RideMarshal) (port.SavedResponse, error)

// rideCommandToken builds the command's dedup identity (ADR-024). The key
// rules live next to the model (idempotency.ValidateIdempotencyToken); the
// hash covers method + request URI + the raw body, so the same key on a
// different ride, on any altered request — or on the same ride with different
// body bytes — is the 409 idempotency conflict (R-KERNEL-002: the same raw
// body bytes are part of the identity, whether or not the command reads them).
// The claim is scoped to the caller (R-KERNEL-003): the token carries the
// authenticated principal's id, so two callers that pick the same key hold two
// claims. ok=false means the header failed validation, the request carried no
// principal (the routes are permission-gated, so this is defensive), or the
// body could not be read — and the response is already written.
// rule:R-GAME-020
// rule:R-KERNEL-002
func (h *Handler) rideCommandToken(w http.ResponseWriter, r *http.Request, endpoint string) (port.Token, bool) {
	user, found := httpx.GetAuthenticatedUser(r)
	if !found {
		httpx.AuthenticationRequired(w, r)
		return port.Token{}, false
	}

	key := r.Header.Get("Idempotency-Key")

	// The body is drained exactly like createRideToken's: the cap must wrap it
	// before the read, so the bytes hashed are the bytes a retry re-reads and
	// an oversized body claims no key at all (413 before any hash exists). The
	// commands do not decode a body today, but the digest must not depend on
	// that — a body appended to a body-less command is a different request.
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.RequestTooLarge(w, r, maxBytesError.Limit)
			return port.Token{}, false
		}
		httpx.BadRequest(w, r, err)
		return port.Token{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	hash := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))

	token := idempotency.IdempotencyToken{Key: key, UserID: user.ID, Endpoint: endpoint, Hash: hash[:]}

	v := validator.New()

	if idempotency.ValidateIdempotencyToken(v, token); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return port.Token{}, false
	}

	return port.Token(token), true
}

// rideCommandHandler is the shared body of the three ride command endpoints
// (lock, burn, unlock): every ride command is idempotent (ADR-024), so each
// runs under an Idempotency-Key and a retry of an executed command replays the
// stored response.
func (h *Handler) rideCommand(endpoint string, command rideCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := httpx.ReadIDParam(r)
		if err != nil {
			httpx.NotFound(w, r)
			return
		}

		token, ok := h.rideCommandToken(w, r, endpoint)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
		defer cancel()

		res, err := command(ctx, id, token, rideMarshal)
		if err != nil {
			h.gameErrorResponse(w, r, err)
			return
		}

		httpx.WriteRawJSON(w, res.Status, res.Body)
	}
}

// createRideToken builds the create command's dedup identity (ADR-024).
// Create is the first ride command carrying a body, so its raw bytes join the
// hash input (method + "\n" + request URI + "\n" + body): the same key
// re-sent with different fields is the 409 idempotency conflict, not a silent
// replay of the first ride. The body is consumed only once, so it is drained
// into memory and handed back on r.Body for readJSON — rideCommandToken keeps
// the same input shape for the body-less commands. The claim is scoped to the
// caller (R-KERNEL-003), so the token carries the authenticated principal's
// id. ok=false means the request carried no principal (defensive: the route is
// permission-gated) or the token failed validation, and the 422/401 is
// written.
// rule:R-GAME-020
// rule:R-KERNEL-002
// rule:R-KERNEL-012
func (h *Handler) createRideToken(w http.ResponseWriter, r *http.Request) ([]byte, port.Token, bool) {
	user, found := httpx.GetAuthenticatedUser(r)
	if !found {
		httpx.AuthenticationRequired(w, r)
		return nil, port.Token{}, false
	}

	// The size cap must wrap the body before it is drained, not at decode:
	// create hashes raw request bytes (ADR-024), so hash input and decoded
	// body must be the same bytes. Capping only in readJSON would buffer an
	// oversized body here in full, store its hash if the decode somehow
	// passed, and then truncate the retry's re-read into a hash mismatch — a
	// 409 on the exact bytes that once succeeded. MaxBytesReader instead
	// hard-stops the read at httpx.MaxBodyBytes and requests connection close.
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.RequestTooLarge(w, r, maxBytesError.Limit)
			return nil, port.Token{}, false
		}
		httpx.BadRequest(w, r, err)
		return nil, port.Token{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	hash := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))

	token := idempotency.IdempotencyToken{
		Key:      r.Header.Get("Idempotency-Key"),
		UserID:   user.ID,
		Endpoint: "POST /v1/rides",
		Hash:     hash[:],
	}

	v := validator.New()

	if idempotency.ValidateIdempotencyToken(v, token); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return nil, port.Token{}, false
	}

	return body, port.Token(token), true
}

// rule:R-GAME-020
func (h *Handler) createRide(w http.ResponseWriter, r *http.Request) {
	// The token first: the key must exist (the retry promise) and the body
	// must hash before decode, so a replay of the same key with altered bytes
	// is the key-misuse 409, never an executed second ride.
	_, idemToken, ok := h.createRideToken(w, r)
	if !ok {
		return
	}

	var input struct {
		PlayerID     int   `json:"player_id"`
		TeamID       int   `json:"team_id"`
		MatchID      int   `json:"match_id"`
		TokensLocked int64 `json:"tokens_locked"`
	}

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	// TODO: ledgerToken := app.Ledger.Tokens.GetByTeamID(input.TeamID)
	ledgerToken := struct {
		ID   int
		Base decimal.Decimal
	}{
		ID:   1,
		Base: decimal.NewFromFloat(1.95),
	}

	ride := domain.Ride{
		PlayerID:     input.PlayerID,
		TeamID:       input.TeamID,
		MatchID:      input.MatchID,
		TokensLocked: decimal.NewFromInt(input.TokensLocked),
		BaseAtLock:   ledgerToken.Base,
	}

	v := validator.New()

	if domain.ValidateRide(v, ride); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	// Every ride command is idempotent (ADR-024), create included: the
	// command claims the key inside its transaction and replays the stored
	// response byte-identical on a retry. Location is derived from the
	// response body's ride id, so the replay answers with the same header
	// without the game layer persisting headers.
	res, err := h.svc.RideCreate(ctx, ride, idemToken, rideCreateMarshal)
	if err != nil {
		h.gameErrorResponse(w, r, err)
		return
	}

	var stored struct {
		Ride struct {
			ID int `json:"id"`
		} `json:"ride"`
	}
	if err := json.Unmarshal(res.Body, &stored); err != nil {
		httpx.ServerError(w, r, h.log, err)
		return
	}
	if stored.Ride.ID != 0 {
		w.Header().Set("Location", fmt.Sprintf("/v1/rides/%d", stored.Ride.ID))
	}

	httpx.WriteRawJSON(w, res.Status, res.Body)
}
