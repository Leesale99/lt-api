package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	"lt-api.aleksrdvn.com/internal/api"
	"lt-api.aleksrdvn.com/internal/config"
	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/ledger"
	"lt-api.aleksrdvn.com/internal/logging"
	"lt-api.aleksrdvn.com/internal/mailer"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// Context role names (migration 000018). Every pool the server opens is
// bound to one bounded context and runs SET ROLE <role> on each of its
// connections, so the session's effective privileges are the context
// role's — the schema boundary (ADR-028, made physical in Phase D) is
// enforced by Postgres, not by code review. Each role's search_path
// resolves the stores' unqualified SQL to its own schema.
const (
	roleSchedule = "schedule_app"
	roleGame     = "game_app"
	roleIdentity = "identity_app"
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

	pools, err := openPools(cfg, logger)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	defer pools.Schedule.Close()
	defer pools.Game.Close()
	defer pools.Identity.Close()

	logger.Info("database connection pools established (schedule/game/identity)")

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

	// Composition root (Phase B/C/D split): each context store is wired to
	// ITS OWN pool (schedule → schedule pool, game → game pool, identity →
	// identity pool); every pool's connections run as the context's role,
	// so a cross-context query fails on permission even when the name is
	// resolvable. The game service consumes the schedule store for the
	// calendar lookups its operations perform (phase windows, next-match
	// pick); game → schedule is the only legal import direction between the
	// two contexts (ADR-028) — its in-transaction round guards run as
	// schedule's SECURITY DEFINER functions (migration 000018), because
	// game's role holds no schedule table privileges at all. The ledger
	// writes only game rows and runs inside the caller's (game pool)
	// transaction — same pool, same role.
	eventBus := eventbus.New()
	scheduleStore := schedule.NewStore(pools.Schedule)
	scheduleService := schedule.NewService(scheduleStore, eventBus, logger)
	gameStore := game.NewStore(pools.Game)
	// The ledger is the money authority (phase-05/06): every money mutation
	// the ride commands and settlement handlers need runs as an intent on
	// it, inside the caller's transaction. Phase 06 swaps the
	// implementation to real balances/journal behind the same interface.
	ledgerStore := ledger.NewStore()
	gameService := game.NewService(gameStore, scheduleStore, ledgerStore)
	identityStore := identity.NewStore(pools.Identity)

	// The event seam (ADR-027): schedule publishes post-commit, game
	// settles from the payloads. *schedule.Store satisfies game's
	// SchedulePort structurally, so the wiring above passes the concrete
	// store as the port.
	eventbus.Subscribe(eventBus, func(ctx context.Context, event schedule.MatchResultRecorded) error {
		return gameService.OnMatchResultRecorded(ctx, event)
	})
	eventbus.Subscribe(eventBus, func(ctx context.Context, event schedule.RoundCompleted) error {
		return gameService.OnRoundCompleted(ctx, event)
	})

	app := &api.Application{
		Version:        cfg.Version,
		Env:            cfg.Env,
		Port:           cfg.Port,
		TrustedOrigins: cfg.CORS.Origins,
		RateLimiter:    rateLimiter,
		Logger:         logger,
		RootCtx:        root,
		Game:           gameService,
		Schedule:       scheduleService,
		Identity:       identityStore,
		Mailer:         mailer,
	}

	err = app.Serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

// contextPools carries the one pool per bounded context. Every store is
// wired to exactly its own pool; the migration runner and the seed keep
// their own (unrestricted, owner) connections and are never handed to a
// context store.
type contextPools struct {
	Schedule *pgxpool.Pool
	Game     *pgxpool.Pool
	Identity *pgxpool.Pool
}

// openPools opens the three context pools. Each is derived from the
// server DSN unless its per-context override is set (LT_API_DSN_SCHEDULE /
// LT_API_DSN_GAME / LT_API_DSN_IDENTITY — deployments that split the
// contexts onto separate databases or login roles). The post-connect hook
// runs SET ROLE <context_role>; SET search_path pairs with it (role-level
// ALTER ROLE settings apply only at session start, not on SET ROLE, so
// the hook carries both), and that pairing is what lets every store keep
// unqualified SQL.
func openPools(cfg config.Config, logger *slog.Logger) (contextPools, error) {
	var pools contextPools

	type poolSpec struct {
		role       string
		searchPath string
		override   string // per-context DSN override, empty = derive
		base       string // the server DSN the pool is derived from
		name       string
	}
	specs := []poolSpec{
		{roleSchedule, "schedule", cfg.DB.DSNSchedule, cfg.DB.DSN, "schedule"},
		{roleGame, "game", cfg.DB.DSNGame, cfg.DB.DSN, "game"},
		{roleIdentity, "identity", cfg.DB.DSNIdentity, cfg.DB.DSN, "identity"},
	}

	closeAll := func() {
		// A failed boot must not leak the pools already open; nil-safe
		// because not all pools may exist yet.
		if pools.Schedule != nil {
			pools.Schedule.Close()
		}
		if pools.Game != nil {
			pools.Game.Close()
		}
		if pools.Identity != nil {
			pools.Identity.Close()
		}
	}

	for _, spec := range specs {
		dsn := spec.base
		if spec.override != "" {
			dsn = spec.override
		}
		pool, err := openContextPool(dsn, spec.role, spec.searchPath, cfg, logger)
		if err != nil {
			closeAll()
			return contextPools{}, fmt.Errorf("open %s pool: %w", spec.name, err)
		}

		switch spec.role {
		case roleSchedule:
			pools.Schedule = pool
		case roleGame:
			pools.Game = pool
		case roleIdentity:
			pools.Identity = pool
		}

		// Fail fast on an unusable pool (bad credentials, missing
		// membership in the context role, unreachable server) instead of
		// at the first request.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = pool.Ping(ctx)
		cancel()
		if err != nil {
			closeAll()
			return contextPools{}, fmt.Errorf("ping %s pool: %w", spec.name, err)
		}
	}

	return pools, nil
}

// openContextPool builds one pool bound to one context role: every
// connection it opens runs SET ROLE <role>; SET search_path pairs with
// it (see openPools). The tracer and pool limits are set on the parsed
// config BEFORE NewWithConfig, and the pool connects lazily, so a
// post-New mutation would be a race against the first acquire.
func openContextPool(dsn, role, searchPath string, cfg config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConns = int32(cfg.DB.MaxConns)
	poolCfg.MaxConnIdleTime = cfg.DB.MaxIdleTime

	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return fmt.Errorf("SET ROLE %s: %w", role, err)
		}
		if _, err := conn.Exec(ctx, "SET search_path = "+searchPath+", public"); err != nil {
			return fmt.Errorf("SET search_path for %s: %w", role, err)
		}
		return nil
	}

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

	return pgxpool.NewWithConfig(context.Background(), poolCfg)
}
