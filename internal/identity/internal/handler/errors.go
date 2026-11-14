package handler

// The identity handlers' error side: identity-specific sentinels map to the
// generic envelope writers in httpx, and server errors go through this one
// helper so every path logs the same way. There are no identity-flavored
// messages of its own — the field-error messages ("user has already been
// activated", "invalid or expired activation token") are the pre-refactor
// handlers' verbatim, added to a validator at the call site.

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	httpx.ServerError(w, r, h.log, err)
}
