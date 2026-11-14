package api

import (
	"context"
	"errors"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) createActivationTokenHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	if identity.ValidateEmail(v, input.Email); !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	user, err := app.Identity.Users.GetByEmail(ctx, input.Email)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.NotFound(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	if user.Activated {
		v.AddError("email", "user has already been activated")
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	userToken, err := app.Identity.UserTokens.New(ctx, user.ID, constants.ActivationTokenTTL, identity.ScopeActivation)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
		return
	}

	app.background(func(ctx context.Context) {
		data := map[string]any{
			"activationToken": userToken.Plaintext,
		}

		err := app.Mailer.Send(ctx, user.Email, "user_token_activation.tmpl", data)
		if err != nil {
			app.Logger.Error(err.Error())
		}
	})

	env := httpx.Envelope{"message": "an email will be sent to you containing activation instructions"}

	err = httpx.WriteJSON(w, http.StatusAccepted, env, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}

func (app *Application) createAuthenticationTokenHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := httpx.ReadJSON(w, r, &input)
	if err != nil {
		httpx.BadRequest(w, r, err)
		return
	}

	v := validator.New()

	identity.ValidateEmail(v, input.Email)
	identity.ValidatePasswordPlaintext(v, input.Password)

	if !v.Valid() {
		httpx.FailedValidation(w, r, v.Errors)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), constants.DBTimeout)
	defer cancel()

	user, err := app.Identity.Users.GetByEmail(ctx, input.Email)
	if err != nil {
		switch {
		case errors.Is(err, persistence.ErrRecordNotFound):
			httpx.InvalidCredentials(w, r)
		default:
			httpx.ServerError(w, r, app.Logger, err)
		}
		return
	}

	match, err := user.Password.Matches(input.Password)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
		return
	}
	if !match {
		httpx.InvalidCredentials(w, r)
		return
	}

	userToken, err := app.Identity.UserTokens.New(ctx, user.ID, constants.AuthenticationTokenTTL, identity.ScopeAuthentication)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
		return
	}

	err = httpx.WriteJSON(w, http.StatusCreated, httpx.Envelope{"user_token": userToken}, nil)
	if err != nil {
		httpx.ServerError(w, r, app.Logger, err)
	}
}
