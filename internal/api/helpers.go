package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

type envelope map[string]any

// sortSafelistNameID is the sort safelist shared by list endpoints whose
// resources are sortable by id or name only.
var sortSafelistNameID = []string{"id", "name", "-id", "-name"}

// sortSafelistID is the sort safelist for list endpoints whose resources
// have no name column (e.g. seasons).
var sortSafelistID = []string{"id", "-id"}

// readListFilters parses the standard list-endpoint query params (page,
// page_size, sort) into store.Filters and validates them against the given
// safelist. On validation failure it writes the 422 response itself and
// returns ok=false — the handler must return immediately. Resource-specific
// filters (name, favorite_team_id, ...) are read by the handler beforehand,
// using the same v so their errors land in the same 422 response.
func (app *Application) readListFilters(w http.ResponseWriter, r *http.Request, qs url.Values, v *validator.Validator, sortSafelist []string) (store.Filters, bool) {
	filters := store.Filters{
		Page:         app.readInt(qs, "page", constants.DefaultPage, v),
		PageSize:     app.readInt(qs, "page_size", constants.DefaultPageSize, v),
		Sort:         app.readString(qs, "sort", "id"),
		SortSafelist: sortSafelist,
	}

	if store.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return filters, false
	}

	return filters, true
}

// writeListResponse is the shared tail of every list endpoint: a canceled
// context means the client hung up (nothing to write), any other store
// error becomes a 500, and success is a 200 envelope of {key: items,
// metadata}. Generic methods are a Go 1.27 language feature (interface
// methods still cannot have type parameters).
func (app *Application) writeListResponse[T any](w http.ResponseWriter, r *http.Request, key string, items []T, metadata store.Metadata, err error) {
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	if err := app.writeJSON(w, http.StatusOK, envelope{key: items, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) readIDParam(r *http.Request, idParam ...string) (int, error) {
	key := "id"
	if len(idParam) > 0 {
		key = idParam[0]
	}
	params := httprouter.ParamsFromContext(r.Context())

	id, err := strconv.Atoi(params.ByName(key))

	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid %s", key)
	}

	return id, nil
}

func (app *Application) writeJSON(w http.ResponseWriter, status int, data envelope, headers http.Header) error {
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
	w.Write(js)

	return nil
}

func (app *Application) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1_048_576)

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
			return fmt.Errorf("body must not be larger then %d bytes", maxBytesError.Limit)

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

func (app *Application) readString(qs url.Values, key string, defaultValue string) string {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}

	return s
}

func (app *Application) readCSV(qs url.Values, key string, defaultValues []string) []string {
	csv := qs.Get(key)
	if csv == "" {
		return defaultValues
	}

	return strings.Split(csv, ",")
}

func (app *Application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
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

// background runs fn on a tracked goroutine and hands it the process-wide
// cancel root: fn must honor ctx cancellation (it does not extend the
// request's lifetime — by the time fn runs, the request may already be done).
func (app *Application) background(fn func(ctx context.Context)) {
	app.wg.Go(func() {
		defer func() {
			pv := recover()
			if pv != nil {
				app.Logger.Error(fmt.Sprintf("%v", pv))
			}
		}()

		fn(app.RootCtx)
	})
}
