package httpx

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
)

// SortSafelistNameID is the sort safelist shared by list endpoints whose
// resources are sortable by id or name only.
var SortSafelistNameID = []string{"id", "name", "-id", "-name"}

// SortSafelistID is the sort safelist for list endpoints whose resources
// have no name column (e.g. seasons).
var SortSafelistID = []string{"id", "-id"}

// ReadIDParam reads a numeric path parameter (default key "id", or the
// caller's key for nested resources) and rejects anything below 1 — a
// non-numeric or non-positive id can never name a row, so the caller answers
// 404 instead of querying.
func ReadIDParam(r *http.Request, idParam ...string) (int, error) {
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
