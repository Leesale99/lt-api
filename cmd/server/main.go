package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/api"
	"lt-api.aleksrdvn.com/internal/config"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/mailer"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Config is born in one place (ADR-014): Parse assembles env/flags and
	// validates everything; main makes no config decisions and owns only
	// the exit.
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	// Construct the mailer before dialing anything: it validates SMTP config,
	// so config errors fail before connection errors.
	mailer, err := mailer.New(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, cfg.SMTP.Password, cfg.SMTP.Sender)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	pool, err := openPool(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	defer pool.Close()

	logger.Info("database connection pool established")

	// Process-level cancel tree: the first SIGTERM/SIGINT cancels the signal
	// context, which cascades to root, which cascades to all background work
	// derived from Application.RootCtx. root exists so Serve's early-exit
	// paths and tests can also cancel everything without a signal.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root, cancel := context.WithCancel(ctx)
	defer cancel()

	rateLimiter := api.NewRateLimiter(
		cfg.Limiter.RPS,
		cfg.Limiter.Burst,
		cfg.Limiter.Enabled,
	)

	app := &api.Application{
		Version:        cfg.Version,
		Env:            cfg.Env,
		Port:           cfg.Port,
		TrustedOrigins: cfg.CORS.Origins,
		RateLimiter:    rateLimiter,
		Logger:         logger,
		RootCtx:        root,
		Game:           game.NewStore(pool),
		Identity:       identity.NewStore(pool),
		Mailer:         mailer,
	}

	err = app.Serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func openPool(cfg config.Config) (*pgxpool.Pool, error) {
	dbpool, err := pgxpool.New(context.Background(), cfg.DB.DSN)
	if err != nil {
		return nil, err
	}

	dbpool.Config().MaxConns = int32(cfg.DB.MaxConns)
	dbpool.Config().MaxConnIdleTime = cfg.DB.MaxIdleTime

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = dbpool.Ping(ctx)
	if err != nil {
		dbpool.Close()
		return nil, err
	}

	return dbpool, nil
}
