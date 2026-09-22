package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	"lt-api.aleksrdvn.com/internal/api"
	"lt-api.aleksrdvn.com/internal/config"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/logging"
	"lt-api.aleksrdvn.com/internal/mailer"
)

func main() {
	// Bootstrap logger: plain JSON at info. It exists only to report failures
	// that happen before configuration is readable — its level and env-based
	// options are unknowable at this point. Everything after config.Parse
	// logs through the real logger built from cfg.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Config is born in one place (ADR-014): Parse assembles env/flags and
	// validates everything; main makes no config decisions and owns only
	// the exit.
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	// The real logger: level from config, source call sites in development.
	logger = logging.New(cfg.LogLevel, cfg.Env)

	// Construct the mailer before dialing anything: it validates SMTP config,
	// so config errors fail before connection errors.
	mailer, err := mailer.New(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, cfg.SMTP.Password, cfg.SMTP.Sender)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	pool, err := openPool(cfg, logger)
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

func openPool(cfg config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	// ParseConfig over New: the tracer and pool limits must be set on the
	// parsed config BEFORE NewWithConfig, and the pool connects lazily, so a
	// post-New mutation would be a race against the first acquire.
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.DSN)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConns = int32(cfg.DB.MaxConns)
	poolCfg.MaxConnIdleTime = cfg.DB.MaxIdleTime

	// Database events flow through the same JSON stream as application logs.
	// pgx only logs via tracers; tracelog.TraceLog implements them all and
	// forwards to slog at its own threshold — warn, so normal query traffic
	// stays silent and connect/query errors still surface.
	poolCfg.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger:   logging.Pgx(logger),
		LogLevel: tracelog.LogLevelWarn,
		// tracelog's default TimeKey is "time", which collides with the JSON
		// handler's own timestamp key — a record with two "time" fields is
		// ambiguous to any parser. The value is an elapsed duration, so the
		// replacement key names both the meaning and the unit.
		Config: &tracelog.TraceLogConfig{TimeKey: "elapsed_ns"},
	}

	dbpool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = dbpool.Ping(ctx)
	if err != nil {
		dbpool.Close()
		return nil, err
	}

	return dbpool, nil
}
