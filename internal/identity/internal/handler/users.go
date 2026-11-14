package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (h *Handler) registerUser(w http.ResponseWriter, r *http.Request) {
	var input registerUserInput

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	// Validate BEFORE hashing: bcrypt errors on input over 72 bytes, and a
	// hashing error would surface as a 500 instead of a validation error.
	// (The service hashes; this handler's job is to have said no first.)
	model.ValidateRegistration(v, input.Name, input.Email, input.Password)
	if !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	user, err := h.svc.Register(ctx, model.User{Name: input.Name, Email: input.Email}, input.Password)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDuplicateEmail):
			v.AddError("email", "a user with this email address already exists")
			httpx.FailedValidation(w, r, v.Errors)
		default:
			h.serverError(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/users/%d", user.ID))

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"user": newUserResponse(user)}, headers)
	if err != nil {
		h.serverError(w, r, err)
	}
}

func (h *Handler) activateUser(w http.ResponseWriter, r *http.Request) {
	var input activateUserInput

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	model.ValidateUserTokenPlaintext(v, input.TokenPlaintext)
	if !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	// The use case owns the transaction (ADR-025): the flip and the token
	// cleanup are one unit of persistence, so the handler choreographs one
	// call instead of three store calls.
	user, err := h.svc.Activate(ctx, input.TokenPlaintext)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, model.ErrNotFound):
			// Unknown, expired or out-of-scope token — the same 422 the
			// handler produced when the lookup was its own store call.
			v.AddError("user_token", "invalid or expired activation token")
			httpx.FailedValidation(w, r, v.Errors)
		case errors.Is(err, model.ErrEditConflict):
			httpx.EditConflict(w, r)
		default:
			h.serverError(w, r, err)
		}
		return
	}

	err = httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{"user": newUserResponse(user)}, nil)
	if err != nil {
		h.serverError(w, r, err)
	}
}
