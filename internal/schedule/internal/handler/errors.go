package handler

// The schedule-flavored response writers: the messages encode rules this
// context owns (ADR-007 delete refusals, ADR-008 lifecycle freezes, unique
// values). The generic status writers live in httpx (httpx/errors.go); the
// envelope and its logging path are shared through httpx.WriteError, so
// nothing is duplicated.

import (
	"net/http"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// writeError is the envelope writer for the domain-flavored responses below:
// the message and status are the handler's, the body shape is httpx's. A
// marshal failure can only be the payload's fault, so it is logged here.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message any) {
	err := httpx.WriteError(w, status, message)
	if err != nil {
		httpx.LogError(h.log, r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (h *Handler) recordInUseResponse(w http.ResponseWriter, r *http.Request) {
	message := "the record is referenced by other records and cannot be deleted"
	h.writeError(w, r, http.StatusConflict, message)
}

// recordFrozenResponse answers the ADR-008 freeze gates: the write tried to
// move something back to an earlier stage while a match has already started.
// A separate message from recordInUseResponse — the conflict is temporal,
// not referential, and "try again later" would be wrong advice.
func (h *Handler) recordFrozenResponse(w http.ResponseWriter, r *http.Request) {
	message := "a match has already started, so the hierarchy cannot move to an earlier stage"
	h.writeError(w, r, http.StatusConflict, message)
}

func (h *Handler) duplicateRecordResponse(w http.ResponseWriter, r *http.Request) {
	message := "a record with these unique values already exists"
	h.writeError(w, r, http.StatusConflict, message)
}
