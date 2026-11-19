package handler

// The request and response shapes of identity's wire surface, copied
// verbatim from the pre-refactor api handlers (json tags included). The
// model carries no json tags — the frozen HTTP format is a presentation
// concern and lives here, not in the entity.

import (
	"time"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

// Request bodies.

// rule:R-IDENTITY-012
// rule:R-IDENTITY-019
// rule:R-IDENTITY-020
type registerUserInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type activateUserInput struct {
	TokenPlaintext string `json:"user_token"`
}

type createActivationTokenInput struct {
	Email string `json:"email"`
}

type createAuthenticationTokenInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Response bodies. Fields the entity marks json:"-" (password, version) are
// simply absent: they never crossed the wire.

type userResponse struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Activated bool      `json:"activated"`
	RoleID    int       `json:"role_id"`
}

// rule:R-IDENTITY-004
func newUserResponse(user model.User) userResponse {
	return userResponse{
		ID:        user.ID,
		CreatedAt: user.CreatedAt,
		Name:      user.Name,
		Email:     user.Email,
		Activated: user.Activated,
		RoleID:    user.RoleID,
	}
}

type userTokenResponse struct {
	Plaintext string    `json:"user_token"`
	Expiry    time.Time `json:"expiry"`
}

func newUserTokenResponse(userToken model.UserToken) userTokenResponse {
	return userTokenResponse{
		Plaintext: userToken.Plaintext,
		Expiry:    userToken.Expiry,
	}
}
