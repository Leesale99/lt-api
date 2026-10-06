package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) registerUserHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	user := identity.User{
		Name:      input.Name,
		Email:     input.Email,
		Activated: false,
	}

	v := validator.New()

	// Validate BEFORE hashing: bcrypt errors on input over 72 bytes, and a
	// hashing error would surface as a 500 instead of a validation error.
	if identity.ValidateRegistration(v, input.Name, input.Email, input.Password); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = user.Password.Set(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	user, err = app.Identity.Users.Insert(ctx, user)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrDuplicateEmail):
			v.AddError("email", "a user with this email address already exists")
			app.failedValidationResponse(w, r, v.Errors)
			return
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	userToken, err := app.Identity.UserTokens.New(ctx, user.ID, constants.ActivationTokenTTL, identity.ScopeActivation)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	app.background(func(ctx context.Context) {
		data := map[string]any{
			"name":            user.Name,
			"activationToken": userToken.Plaintext,
		}

		err := app.Mailer.Send(ctx, user.Email, "user_welcome.tmpl", data)
		if err != nil {
			app.Logger.Error(err.Error())
		}
	})

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/users/%d", user.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"user": user}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) activateUserHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TokenPlaintext string `json:"user_token"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()

	if identity.ValidateUserTokenPlaintext(v, input.TokenPlaintext); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	// The use case owns the transaction (ADR-025): the flip and the token
	// cleanup are one unit of persistence, so the handler choreographs one
	// call instead of three store calls.
	user, err := app.Identity.Activate(ctx, input.TokenPlaintext)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, store.ErrRecordNotFound):
			// Unknown, expired or out-of-scope token — the same 422 the
			// handler produced when the lookup was its own store call.
			v.AddError("user_token", "invalid or expired activation token")
			app.failedValidationResponse(w, r, v.Errors)
		case errors.Is(err, store.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"user": user}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
