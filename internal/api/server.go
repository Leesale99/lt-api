package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Application struct {
	Version string
	Port    int
	Env     string
	Logger  *slog.Logger
}

func (app *Application) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthcheck", app.healthcheckHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.Port),
		Handler:      mux,
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		ErrorLog:     slog.NewLogLogger(app.Logger.Handler(), slog.LevelError),
	}

	app.Logger.Info("starting server", "addr", srv.Addr, "env", app.Env)

	err := srv.ListenAndServe()
	if err != nil {
		return err
	}

	return nil
}
