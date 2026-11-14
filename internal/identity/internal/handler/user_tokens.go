package handler

import (
	"context"
	"errors"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/validator"
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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	err = h.svc.RequestActivationToken(ctx, input.Email)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrNotFound):
			httpx.NotFound(w, r)
		case errors.Is(err, model.ErrAlreadyActivated):
			v.AddError("email", "user has already been activated")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			h.serverError(w, r, err)
		}
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

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	userToken, err := h.svc.Authenticate(ctx, input.Email, input.Password)
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
