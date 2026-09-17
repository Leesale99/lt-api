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
	"lt-api.aleksrdvn.com/internal/mailer"
)

type Application struct {
	Version  string
	Port     int
	Env      string
	Logger   *slog.Logger
	Game     *game.Store
	Identity *identity.Store
	Mailer   *mailer.Mailer
	wg       sync.WaitGroup
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

		shutdownError <- srv.Shutdown(context.Background())
	}()

	app.Logger.Info("starting server", "addr", srv.Addr, "env", app.Env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		return err
	}

	app.Logger.Info("waiting for background tasks")

	app.wg.Wait()

	app.Logger.Info("shutdown complete")
	return nil
}
