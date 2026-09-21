package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
)

type Application struct {
	Version string
	Port    int
	Env     string
	// RateLimiter is nil when rate limiting is not wired (handler tests);
	// constructed with Enabled=false it passes everything through.
	RateLimiter *RateLimiter
	Logger      *slog.Logger
	// RootCtx is the process-wide cancel root: canceled on SIGTERM/SIGINT
	// (main wires it to the signal context) and handed to every background
	// task started via background(). It is set once at construction and
	// never mutated — a dependency like Logger, not ambient mutable state.
	RootCtx context.Context
	// Shutdown budgets. Zero means "use the constants default" — main relies
	// on that; tests set short values so shutdown scenarios run in ms.
	ShutdownGracePeriod  time.Duration
	BackgroundTaskBudget time.Duration

	Game     *game.Store
	Identity *identity.Store
	Mailer   MailSender
	wg       sync.WaitGroup
}

// gracePeriod is the HTTP-drain budget: field override if set, else the
// constants default.
func (app *Application) gracePeriod() time.Duration {
	if app.ShutdownGracePeriod > 0 {
		return app.ShutdownGracePeriod
	}
	return constants.ShutdownGracePeriod
}

// taskBudget is the background-goroutine wait budget, same override rule.
func (app *Application) taskBudget() time.Duration {
	if app.BackgroundTaskBudget > 0 {
		return app.BackgroundTaskBudget
	}
	return constants.BackgroundTaskBudget
}

// MailSender is everything Application needs from the mailer: one method.
// Declaring the interface here (at the consumer, not next to the concrete
// type) keeps Application decoupled from SMTP entirely — tests inject a
// no-op instead of a live client.
type MailSender interface {
	Send(ctx context.Context, recipient string, templateFile string, data any) error
}

func (app *Application) Serve() error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.Port),
		Handler:      app.routes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		ErrorLog:     slog.NewLogLogger(app.Logger.Handler(), slog.LevelError),
	}

	return app.runServer(srv)
}

// runServer is Serve's testable core: the full shutdown lifecycle (signal
// reaction, escalation, drain, bounded background wait) decoupled from how
// the http.Server and its handler are built. Tests inject a server with a
// hanging handler; production passes the one built in Serve.
func (app *Application) runServer(srv *http.Server) error {
	// The rate-limiter sweep lives exactly as long as serving: started here
	// (once per process — runServer is the lifecycle owner), canceled with
	// RootCtx, waited on by the bounded wg.Wait during shutdown. routes()
	// stays a pure builder; never start goroutines there.
	if app.RateLimiter != nil && app.RateLimiter.Enabled {
		app.background(app.RateLimiter.Sweep)
	}

	shutdownError := make(chan error)

	go func() {
		<-app.RootCtx.Done()

		app.Logger.Info("stopping server", "addr", srv.Addr)

		// Second-signal escalation: a further SIGTERM/SIGINT during the drain
		// force-closes the server instead of being swallowed by the (already
		// canceled) signal context. signal.Notify stacks with NotifyContext's
		// registration, so arming this now catches the next signal.
		secondSignal := make(chan os.Signal, 1)
		signal.Notify(secondSignal, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(secondSignal)

		drainDone := make(chan struct{})
		go func() {
			select {
			case s := <-secondSignal:
				app.Logger.Warn("second signal during drain, force-closing", "signal", s.String())
				srv.Close()
			case <-drainDone:
			}
		}()

		shutdownError <- app.shutdown(srv)

		close(drainDone) // drain over: the watcher goroutine can exit
	}()

	app.Logger.Info("starting server", "addr", srv.Addr, "env", app.Env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// The signal goroutine owns draining; its result (drained, force-closed,
	// or a real failure) is the server's shutdown result.
	err = <-shutdownError
	if err != nil {
		return err
	}

	app.Logger.Info("waiting for background tasks")

	// Bounded wait: background tasks were handed the (already canceled)
	// cancel root, so well-behaved ones finish fast — this budget only
	// bounds misbehaving ones. Timeout is not an error: the shutdown did
	// complete, tasks were abandoned (and said so in the log).
	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(app.taskBudget()):
		app.Logger.Warn("background tasks exceeded budget, exiting", "budget", app.taskBudget())
	}

	app.Logger.Info("shutdown complete")
	return nil
}

// shutdown drains active connections within a fixed budget, then force-closes
// whatever is left. A completed force-close after a drain timeout is not an
// error — the server did stop, just ungracefully — so only a failing force
// close (or a non-timeout drain failure) is surfaced as an error.
func (app *Application) shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.gracePeriod())
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	app.Logger.Warn("drain exceeded budget, force-closing connections", "budget", app.gracePeriod())

	if closeErr := srv.Close(); closeErr != nil {
		app.Logger.Error("force close failed", "error", closeErr)
		return closeErr
	}

	return nil
}
