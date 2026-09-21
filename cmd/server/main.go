package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/api"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/mailer"
)

const version = "1.0.0"

type config struct {
	port int
	env  string
	db   struct {
		dsn         string
		maxConns    int
		maxIdleTime time.Duration
	}
	limiter struct {
		rps     float64
		burst   int
		enabled bool
	}
	smtp struct {
		host     string
		port     int
		username string
		password string
		sender   string
	}
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 9000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")

	flag.StringVar(&cfg.db.dsn, "db-dsn", "", "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxConns, "db-max-conns", 25, "PostgreSQL max open and idle connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 15*time.Minute, "PostgreSQL max connection idle time")

	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", 2, "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", 4, "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", true, "Enable rate limiter")

	flag.StringVar(&cfg.smtp.host, "smtp-host", "sandbox.smtp.mailtrap.io", "SMTP host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 2525, "SMTP port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", "", "SMTP username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", "", "SMTP password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", "League Tokens <no-reply@lt.aleksrdvn.com>", "SMTP sender")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Construct the mailer before dialing anything: it validates SMTP config,
	// so config errors fail before connection errors.
	mailer, err := mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender)
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
		cfg.limiter.rps,
		cfg.limiter.burst,
		cfg.limiter.enabled,
	)

	app := &api.Application{
		Version:     version,
		Env:         cfg.env,
		Port:        cfg.port,
		RateLimiter: rateLimiter,
		Logger:      logger,
		RootCtx:     root,
		Game:        game.NewStore(pool),
		Identity:    identity.NewStore(pool),
		Mailer:      mailer,
	}

	err = app.Serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func openPool(cfg config) (*pgxpool.Pool, error) {
	dbpool, err := pgxpool.New(context.Background(), cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	dbpool.Config().MaxConns = int32(cfg.db.maxConns)
	dbpool.Config().MaxConnIdleTime = cfg.db.maxIdleTime

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = dbpool.Ping(ctx)
	if err != nil {
		dbpool.Close()
		return nil, err
	}

	return dbpool, nil
}
