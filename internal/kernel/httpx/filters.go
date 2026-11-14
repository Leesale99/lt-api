package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// ReadListFilters parses the standard list-endpoint query params (page,
// page_size, sort) into persistence.Filters and validates them against the
// given safelist. On validation failure it writes the 422 response itself and
// returns ok=false — the handler must return immediately. Resource-specific
// filters (name, favorite_team_id, ...) are read by the handler beforehand,
// using the same v so their errors land in the same 422 response.
func ReadListFilters(w http.ResponseWriter, r *http.Request, qs url.Values, v *validator.Validator, sortSafelist []string) (persistence.Filters, bool) {
	filters := persistence.Filters{
		Page:         ReadInt(qs, "page", DefaultPage, v),
		PageSize:     ReadInt(qs, "page_size", DefaultPageSize, v),
		Sort:         ReadString(qs, "sort", "id"),
		SortSafelist: sortSafelist,
	}

	if persistence.ValidateFilters(v, filters); !v.Valid() {
		FailedValidation(w, r, v.Errors)
		return filters, false
	}

	return filters, true
}

// WriteListResponse is the shared tail of every list endpoint: a canceled
// context means the client hung up (nothing to write), any other store
// error becomes a 500, and success is a 200 envelope of {key: items,
// metadata}. Generic methods are a Go 1.27 language feature (interface
// methods still cannot have type parameters).
func WriteListResponse[T any](w http.ResponseWriter, r *http.Request, logger *slog.Logger, key string, items []T, metadata persistence.Metadata, err error) {
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
		default:
			ServerError(w, r, logger, err)
		}
		return
	}

	if err := WriteJSON(w, http.StatusOK, Envelope{key: items, "metadata": metadata}, nil); err != nil {
		ServerError(w, r, logger, err)
	}
}
