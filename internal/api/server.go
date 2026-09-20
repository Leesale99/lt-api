package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"lt-api.aleksrdvn.com/internal/constants"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
)

type Application struct {
	Version string
	Port    int
	Env     string
	Logger  *slog.Logger
	// RootCtx is the process-wide cancel root: canceled on SIGTERM/SIGINT
	// (main wires it to the signal context) and handed to every background
	// task started via background(). It is set once at construction and
	// never mutated — a dependency like Logger, not ambient mutable state.
	RootCtx  context.Context
	Game     *game.Store
	Identity *identity.Store
	Mailer   MailSender
	wg       sync.WaitGroup
}

// MailSender is everything Application needs from the mailer: one method.
// Declaring the interface here (at the consumer, not next to the concrete
// type) keeps Application decoupled from SMTP entirely — tests inject a
// no-op instead of a live client.
type MailSender interface {
	Send(recipient string, templateFile string, data any) error
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

	shutdownError := make(chan error)

	go func() {
		<-app.RootCtx.Done()

		app.Logger.Info("stopping server", "addr", srv.Addr)

		shutdownError <- app.shutdown(srv)
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

	app.wg.Wait()

	app.Logger.Info("shutdown complete")
	return nil
}

// shutdown drains active connections within a fixed budget, then force-closes
// whatever is left. A completed force-close after a drain timeout is not an
// error — the server did stop, just ungracefully — so only a failing force
// close (or a non-timeout drain failure) is surfaced as an error.
func (app *Application) shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), constants.ShutdownGracePeriod)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	app.Logger.Warn("drain exceeded budget, force-closing connections", "budget", constants.ShutdownGracePeriod)

	if closeErr := srv.Close(); closeErr != nil {
		app.Logger.Error("force close failed", "error", closeErr)
		return closeErr
	}

	return nil
}
