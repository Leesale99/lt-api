package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"lt-api.aleksrdvn.com/internal/api"
)

const version = "1.0.0"

type config struct {
	port int
	env  string
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (deevlopment|staging|production")
	fmt.Println("Hello World")

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	app := &api.Application{
		Version: version,
		Env:     cfg.env,
		Port:    cfg.port,
		Logger:  logger,
	}

	err := app.Serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
