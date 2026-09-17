package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) registerUser(w http.ResponseWriter, r *http.Request) {
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

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/users/%d", user.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"user": user}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
