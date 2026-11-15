package httpx

// Middleware tests. RecoverPanic is the only middleware whose contract is
// pure transport (a status, a header and a log line), so it is pinned here
// without a store, a database or a chain.
//
// R-KERNEL-011 — a panic is answered as a server error, never explained: the
// client gets ServerError's generic envelope and Connection: close, the cause
// travels to the log only, and nothing about the failure (a panic value, a
// query, a stack) reaches the body.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serverErrorBody is ServerError's exact response: the generic message,
// marshalled by WriteJSON (MarshalIndent + trailing newline). Asserted as
// bytes, not as a decoded field, so a body that grew a second key — a "detail",
// a "cause", a "trace" — fails here instead of passing a subset check.
const serverErrorBody = "{\n\t\"error\": \"the server encountered a problem and could not process your request\"\n}\n"

// TestRecoverPanicAnswersServerError drives Middleware.RecoverPanic with a
// panicking handler and asserts the whole answer: 500, the generic envelope
// byte for byte, Connection: close, and the cause in the log.
//
// Failure mode it exists for: a handler chain that forwards the panic value
// (or the stack recovered with it) into the body. The panic value here names
// a query and a token, so any leak of it into the response is unambiguous.
func TestRecoverPanicAnswersServerError(t *testing.T) {
	const (
		secret = "SELECT token FROM user_tokens WHERE user_id = 42"
		method = http.MethodGet
		uri    = "/v1/probe?token=leak"
	)

	var logBuf bytes.Buffer
	m := &Middleware{Logger: slog.New(slog.NewTextHandler(&logBuf, nil))}

	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("repository exploded: " + secret)
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, uri, nil)

	// If RecoverPanic did not recover, this ServeHTTP call panics and takes
	// the test binary down — the loud failure the rule wants.
	m.RecoverPanic(panicHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	if got := rr.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection header = %q, want %q", got, "close")
	}

	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	if got := rr.Body.String(); got != serverErrorBody {
		t.Errorf("body = %q, want ServerError's generic envelope %q", got, serverErrorBody)
	}

	// Nothing internal reaches the client: not the panic value, not the query
	// it carried, not a stack.
	body := rr.Body.String()
	for _, leak := range []string{secret, "repository exploded", "goroutine", "RecoverPanic", ".go:"} {
		if strings.Contains(body, leak) {
			t.Errorf("body leaks %q: %q", leak, body)
		}
	}

	// The cause is not swallowed: it is recorded, with the request's tags.
	logged := logBuf.String()
	if logged == "" {
		t.Fatal("panic was not logged at all, want the cause recorded via ServerError/LogError")
	}
	// slog quotes the URI (it carries "?" and "="), so the tag and its value
	// are asserted separately.
	for _, want := range []string{"level=ERROR", secret, "method=" + method, "uri=", uri} {
		if !strings.Contains(logged, want) {
			t.Errorf("log %q does not contain %q", logged, want)
		}
	}
}
