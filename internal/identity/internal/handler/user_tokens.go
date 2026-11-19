package handler

import (
	"context"
	"errors"
	"net/http"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

func (h *Handler) createActivationToken(w http.ResponseWriter, r *http.Request) {
	var input createActivationTokenInput

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	model.ValidateEmail(v, input.Email)
	if !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), persistence.DBTimeout)
	defer cancel()

	err = h.svc.RequestActivationToken(ctx, input.Email)
	switch {
	case err == nil:
		// A genuine request: the service minted a token and booked the mail.
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrAlreadyActivated):
		// Existence neutralization (R-IDENTITY-022): an address no account
		// holds and an account that is already active are answered exactly as
		// a genuine request is — same 202, same envelope — because the
		// service minted nothing and mailed nothing. A distinguishable refusal
		// here would turn this endpoint into an account oracle. The neutral
		// answer is the one below; only that the work was skipped differs.
	default:
		h.serverError(w, r, err)
		return
	}

	env := httpx.Envelope{"message": "an email will be sent to you containing activation instructions"}

	err = httpx.WriteJSON(w, http.StatusAccepted, env, nil)
	if err != nil {
		h.serverError(w, r, err)
	}
}

func (h *Handler) createAuthenticationToken(w http.ResponseWriter, r *http.Request) {
	var input createAuthenticationTokenInput

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	model.ValidateEmail(v, input.Email)
	model.ValidatePasswordPlaintext(v, input.Password)

	if !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	// No DB budget is opened here: the use case times its own database
	// sections, so the bcrypt compare on login never spends the budget.
	userToken, err := h.svc.Authenticate(r.Context(), input.Email, input.Password)
	if err != nil {
		if errors.Is(err, model.ErrInvalidCredentials) {
			httpx.InvalidCredentials(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"user_token": newUserTokenResponse(userToken)}, nil)
	if err != nil {
		h.serverError(w, r, err)
	}
}
