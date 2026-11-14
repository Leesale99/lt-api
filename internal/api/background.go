package api

import (
	"context"
	"fmt"
)

// background runs fn on a tracked goroutine and hands it the process-wide
// cancel root: fn must honor ctx cancellation (it does not extend the
// request's lifetime — by the time fn runs, the request may already be done).
//
// It stays with Application (not httpx): the wg it books the goroutine on is
// the waitgroup runServer waits out during shutdown, so the tracker and the
// lifecycle that waits on it are one thing.
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
