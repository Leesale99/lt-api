package api

import (
	"context"
)

// background runs fn on a tracked goroutine and hands it the process-wide
// cancel root: fn must honor ctx cancellation (it does not extend the
// request's lifetime — by the time fn runs, the request may already be done).
//
// The tracker is the shared server's waitgroup — the one Serve waits out
// during shutdown — so this stays a thin delegation: every handler call site
// keeps writing app.background(fn) and the lifecycle that drains it remains
// the single owner of the wait.
func (app *Application) background(fn func(ctx context.Context)) {
	app.Server.Background(fn)
}
