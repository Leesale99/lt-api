package httpx

import "time"

// Transport-process tuning values owned by the HTTP kernel. Change them here,
// not at call sites: every HTTP surface (middleware and module handlers) draws
// its per-request database budget and list defaults from this file, so the
// values cannot drift between modules.

// DBTimeout bounds one database round trip made on behalf of an HTTP request.
// It lives with the transport because its consumers are the middleware's own
// auth lookups and the module handlers — no repository takes a timeout of its
// own, so the kernel's persistence package never needs it.
const DBTimeout = 3 * time.Second

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
