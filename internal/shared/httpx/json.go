// Package httpx is the reusable HTTP machinery behind the service's handlers:
// the JSON envelope and its reader/writer, request-param parsing, list
// filters, the generic error writers, and the middleware chain.
//
// It is transport, not a context: httpx never imports a module,
// schedule, game, identity, ledger or idempotency. Anything contextual —
// resolving a bearer token, reading a role's permissions — reaches it
// through the consumer-side interfaces declared in middleware.go.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lt-api.aleksrdvn.com/internal/validator"
)

// Envelope is the JSON body shape every handler writes: one or two keys, each
// mapped to its payload ({"ride": ...}, {"rides": [...], "metadata": ...},
// {"error": ...}).
type Envelope map[string]any

// MaxBodyBytes caps every request body at 1 MiB. The intake wraps live at
// each reader of r.Body (createRideToken's hash drain, ReadJSON's decode)
// and must agree with this limit — one constant, no drifting copies.
const MaxBodyBytes = 1 << 20

func WriteJSON(w http.ResponseWriter, status int, data Envelope, headers http.Header) error {
	js, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}

	js = append(js, '\n')

	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(js) // headers already sent; nothing left to do with a write error

	return nil
}

// WriteRawJSON emits a pre-marshaled JSON body — the idempotent replay path,
// which must return the stored response byte-identical (ADR-024).
func WriteRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body) // headers already sent; nothing left to do with a write error
}

// ReadJSON decodes exactly one JSON value into dst: unknown fields are
// rejected, the body is capped at MaxBodyBytes, and every failure comes back
// as a client-readable sentence (handlers turn it into a 400).
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var (
			syntaxError           *json.SyntaxError
			unmarshalTypeError    *json.UnmarshalTypeError
			invalidUnmarshalError *json.InvalidUnmarshalError
			maxBytesError         *http.MaxBytesError
		)

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)

		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)

		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return fmt.Errorf("body contains unknown key %s", fieldName)

		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)

		case errors.As(err, &invalidUnmarshalError):
			panic(err)

		default:
			return err
		}
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON value")
	}

	return nil
}

func ReadString(qs url.Values, key string, defaultValue string) string {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}

	return s
}

//lint:ignore U1000 helper currently unused, but might be in the future
func ReadCSV(qs url.Values, key string, defaultValues []string) []string {
	csv := qs.Get(key)
	if csv == "" {
		return defaultValues
	}

	return strings.Split(csv, ",")
}

func ReadInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		v.AddError(key, "must be an integer value")
		return defaultValue
	}

	return i
}
