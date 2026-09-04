package main

import (
	"flag"
	"log/slog"
	"os"

	"lt-api.aleksrdvn.com/internal/api"
	"lt-api.aleksrdvn.com/internal/game"
)

const version = "1.0.0"

type config struct {
	port int
	env  string
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	app := &api.Application{
		Version: version,
		Env:     cfg.env,
		Port:    cfg.port,
		Logger:  logger,
		Game:    game.NewService(),
	}

	err := app.Serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
