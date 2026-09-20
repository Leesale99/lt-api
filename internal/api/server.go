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

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
)

type Application struct {
	Version  string
	Port     int
	Env      string
	Logger   *slog.Logger
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
		quit := make(chan os.Signal, 1)

		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

		s := <-quit

		app.Logger.Info("stopping server", "addr", srv.Addr, "signal", s.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		shutdownError <- srv.Shutdown(ctx)
	}()

	app.Logger.Info("starting server", "addr", srv.Addr, "env", app.Env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		if errors.Is(err, context.Canceled) {
			app.Logger.Warn("shutdown timeout")
		}
		return err
	}

	app.Logger.Info("waiting for background tasks")

	app.wg.Wait()

	app.Logger.Info("shutdown complete")
	return nil
}
