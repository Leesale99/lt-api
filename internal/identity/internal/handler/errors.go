package handler

// The identity handlers' error side: identity-specific sentinels map to the
// generic envelope writers in httpx, and server errors go through this one
// helper so every path logs the same way. There are no identity-flavored
// messages of its own — the one field-error message left ("invalid or expired
// activation token") is the pre-refactor handler's verbatim, added to a
// validator at the call site. The request-activation endpoint no longer adds a
// field error at all: both of its ineligible causes are answered with the
// genuine request's 202 envelope (R-IDENTITY-022).

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	httpx.ServerError(w, r, h.log, err)
}
