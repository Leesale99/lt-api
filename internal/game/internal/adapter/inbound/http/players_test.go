package http

// The player transport's refusal shapes: the checks the handler performs
// itself, including the identity read that guards the user reference. A
// refusal that names the wrong field is a lie to the client, so these tests
// pin the field the caller must actually fix.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"

	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// absentUserDirectory stands in for identity when the principal's user row is
// gone: the read fails with the module's not-found sentinel.
type absentUserDirectory struct{}

func (absentUserDirectory) User(context.Context, int) (identity.User, error) {
	return identity.User{}, identity.ErrNotFound
}

// TestCreatePlayerNamesTheUserField pins the identity refusal's wire shape
// (R-GAME-013's handler site, misalignment-report §6 item 12): the reference
// that failed is the authenticated principal's user account, so the 422 names
// user_id. It used to name favorite_team_id with the team's message — a
// leftover of the FK-era shape that pointed the caller at the wrong field.
func TestCreatePlayerNamesTheUserField(t *testing.T) {
	h := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.UseIdentity(absentUserDirectory{})

	req := httptest.NewRequest(http.MethodPost, "/v1/seasons/1/players",
		strings.NewReader(`{"name":"Ana","favorite_team_id":1}`))
	req = httpx.SetAuthenticatedUser(req, httpx.AuthUser{ID: 7, Activated: true})

	// The route's own parameters are the handler's input; the bare router keeps
	// the middleware chain out of the way.
	router := httprouter.New()
	router.Handler(http.MethodPost, "/v1/seasons/:id/players", http.HandlerFunc(h.createPlayer))

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422 (body: %s)", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"user_id"`) {
		t.Fatalf("the refusal does not name user_id (body: %s)", body)
	}
	if strings.Contains(body, "favorite_team_id") {
		t.Errorf("the refusal still names favorite_team_id, the field that did not fail (body: %s)", body)
	}
}
