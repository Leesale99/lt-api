package httpx

import (
	"fmt"
	"log/slog"
	"net/http"
)

// This file holds the generic status writers: every response the transport
// layer can produce without knowing anything about the domain. Writers whose
// message encodes a domain rule (round not open, invalid phase, idempotency
// conflict, ...) stay with the handlers that own the rule; they render
// through WriteError, the single envelope used here too.
//
// The logger is a parameter, not state: httpx carries no dependencies of its
// own, so the caller's logger (and its "method"/"uri" tags) travels with the
// request.

// LogError logs a handler error with the request's method and URI.
func LogError(logger *slog.Logger, r *http.Request, err error) {
	var (
		method = r.Method
		uri    = r.URL.RequestURI()
	)

	logger.Error(err.Error(), "method", method, "uri", uri)
}

// WriteError writes the standard {"error": message} envelope. The error
// return is the marshal failure only — the caller has already committed to
// this response, so it decides what to do about it (api logs and re-writes
// the status).
func WriteError(w http.ResponseWriter, status int, message any) error {
	return WriteJSON(w, status, Envelope{"error": message}, nil)
}

func ServerError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	LogError(logger, r, err)

	message := "the server encountered a problem and could not process your request"
	if writeErr := WriteError(w, http.StatusInternalServerError, message); writeErr != nil {
		LogError(logger, r, writeErr)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func NotFound(w http.ResponseWriter, r *http.Request) {
	message := "the requested resource could not be found"
	_ = WriteError(w, http.StatusNotFound, message)
}

func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	message := fmt.Sprintf("the %s method is not supported for this resource", r.Method)
	_ = WriteError(w, http.StatusMethodNotAllowed, message)
}

func BadRequest(w http.ResponseWriter, r *http.Request, err error) {
	_ = WriteError(w, http.StatusBadRequest, err.Error())
}

// RequestTooLarge answers a body that exceeded MaxBodyBytes at intake (413):
// the read was cut short before anything was decoded or hashed, so nothing is
// claimed and a retry with a normal body re-executes.
func RequestTooLarge(w http.ResponseWriter, r *http.Request, limit int64) {
	message := fmt.Sprintf("body must not be larger than %d bytes", limit)
	_ = WriteError(w, http.StatusRequestEntityTooLarge, message)
}

func FailedValidation(w http.ResponseWriter, r *http.Request, fieldErrors map[string]string) {
	_ = WriteError(w, http.StatusUnprocessableEntity, fieldErrors)
}

func EditConflict(w http.ResponseWriter, r *http.Request) {
	message := "unable to update the record due to an edit conflict, please try again"
	_ = WriteError(w, http.StatusConflict, message)
}

func InvalidCredentials(w http.ResponseWriter, r *http.Request) {
	message := "invalid authentication credentials"
	_ = WriteError(w, http.StatusUnauthorized, message)
}

func InvalidAuthenticationToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")

	message := "invalid or missing authentication"
	_ = WriteError(w, http.StatusUnauthorized, message)
}

func AuthenticationRequired(w http.ResponseWriter, r *http.Request) {
	message := "you must be authenticated to acces this resource"
	_ = WriteError(w, http.StatusUnauthorized, message)
}

func InactiveAccount(w http.ResponseWriter, r *http.Request) {
	message := "your user account must be activated to access this resource"
	_ = WriteError(w, http.StatusForbidden, message)
}

func MissingPermission(w http.ResponseWriter, r *http.Request) {
	message := "your user account doesn't have the necessary permissions to access this resource"
	_ = WriteError(w, http.StatusForbidden, message)
}

func Forbidden(w http.ResponseWriter, r *http.Request) {
	message := "you do not have permission to modify this resource"
	_ = WriteError(w, http.StatusForbidden, message)
}

func TooManyRequests(w http.ResponseWriter, r *http.Request) {
	message := "rate limit exceeded"
	_ = WriteError(w, http.StatusTooManyRequests, message)
}

// The writers above drop WriteError's marshal failure: they have no logger to
// report it through and their status is already settled (4xx/5xx), so the
// response is committed either way. ServerError — the one writer that must
// log — handles it explicitly.
