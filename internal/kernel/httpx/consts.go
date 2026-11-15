package httpx

import "time"

// Transport-process tuning values owned by the HTTP kernel. Change them here,
// not at call sites: every HTTP surface (middleware and module handlers) draws
// its per-request list defaults and rate-limit sweep interval from this file,
// so the values cannot drift between modules.
//
// The database budget (persistence.DBTimeout) lives in the persistence kernel
// instead: it bounds storage work only, so CPU-bound work between DB calls
// never spends it.

// Pagination defaults for the standard list endpoints, applied when the query
// string omits page/page_size (ReadListFilters is the only reader).
const (
	DefaultPage     = 1
	DefaultPageSize = 20
)

// RateLimitCleanupInterval is how long a client's bucket may sit idle before
// Sweep evicts it. The rate limiter is transport-only, so the value lives
// beside it.
const RateLimitCleanupInterval = 3 * time.Minute
