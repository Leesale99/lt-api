// Package config owns everything about runtime configuration: the variable
// names, the precedence rule, and the semantic validation. The precedence
// (CLI flag > environment > built-in default) and the "fail at boot, name the
// variable" contract are ADR-014; this package is where that decision lives.
// The server binary reduces to config.Parse(os.Args[1:]) plus wiring — main
// makes no config decisions and owns only the exit.
//
// 12-factor: an identical binary is configured by its environment. Flags
// remain for dev convenience; production injects env vars only.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Port int
	Env  string
	DB   struct {
		DSN         string
		MaxConns    int
		MaxIdleTime time.Duration
	}
	Limiter struct {
		RPS     float64
		Burst   int
		Enabled bool
	}
	SMTP struct {
		Host     string
		Port     int
		Username string
		Password string
		Sender   string
	}
	CORS struct {
		// Origins is nil when CORS is disabled (CORS_ORIGINS unset or empty).
		// nil means "no cross-origin browser request is allowed", never "*":
		// an absent allowlist must not degrade into "allow all".
		Origins []string
	}
}

// Parse assembles the configuration from env and args and validates it.
// It is the only entry point: callers never touch flag or env for server
// config, and a returned error is always safe to log verbatim.
func Parse(args []string) (Config, error) {
	env, err := readEnv()
	if err != nil {
		return Config{}, err
	}

	var cfg Config

	// Private FlagSet, not the global CommandLine: no cross-binary or
	// cross-test pollution, and ContinueOnError turns parse failures into
	// ordinary errors instead of an exit buried inside the flag package.
	fs := flag.NewFlagSet("lt-api-server", flag.ContinueOnError)

	fs.IntVar(&cfg.Port, "port", env.port, "API server port (env: PORT)")
	fs.StringVar(&cfg.Env, "env", env.env, "Environment (development|staging|production) (env: ENV)")

	fs.StringVar(&cfg.DB.DSN, "db-dsn", env.dbDSN, "PostgreSQL DSN (env: LT_API_DSN)")
	fs.IntVar(&cfg.DB.MaxConns, "db-max-conns", env.dbMaxConns, "PostgreSQL max open and idle connections (env: LT_API_DB_MAX_CONNS)")
	fs.DurationVar(&cfg.DB.MaxIdleTime, "db-max-idle-time", env.dbMaxIdleTime, "PostgreSQL max connection idle time (env: LT_API_DB_MAX_IDLE_TIME)")

	fs.Float64Var(&cfg.Limiter.RPS, "limiter-rps", env.limiterRPS, "Rate limiter maximum requests per second (env: LT_API_LIMITER_RPS)")
	fs.IntVar(&cfg.Limiter.Burst, "limiter-burst", env.limiterBurst, "Rate limiter maximum burst (env: LT_API_LIMITER_BURST)")
	fs.BoolVar(&cfg.Limiter.Enabled, "limiter-enabled", env.limiterEnabled, "Enable rate limiter (env: LT_API_LIMITER_ENABLED)")

	fs.StringVar(&cfg.SMTP.Host, "smtp-host", env.smtpHost, "SMTP host (env: SMTP_HOST)")
	fs.IntVar(&cfg.SMTP.Port, "smtp-port", env.smtpPort, "SMTP port (env: SMTP_PORT)")
	fs.StringVar(&cfg.SMTP.Username, "smtp-username", env.smtpUsername, "SMTP username (env: SMTP_USERNAME)")
	fs.StringVar(&cfg.SMTP.Password, "smtp-password", env.smtpPassword, "SMTP password (env: SMTP_PASSWORD)")
	fs.StringVar(&cfg.SMTP.Sender, "smtp-sender", env.smtpSender, "SMTP sender (env: SMTP_SENDER)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	// No flag for CORS_ORIGINS: an origin allowlist is deployment data, not
	// an operator override — there is no legitimate reason to inject it per-run.
	cfg.CORS.Origins, err = parseCORSOrigins(env.corsRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid CORS_ORIGINS: %w", err)
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// envDefaults carries the env-derived flag defaults, so every env parse error
// surfaces before flag.Parse ever runs (all bad variables at once), and the
// flag registrations stay plain literals reading from one struct.
type envDefaults struct {
	port           int
	env            string
	dbDSN          string
	dbMaxConns     int
	dbMaxIdleTime  time.Duration
	limiterRPS     float64
	limiterBurst   int
	limiterEnabled bool
	smtpHost       string
	smtpPort       int
	smtpUsername   string
	smtpPassword   string
	smtpSender     string
	corsRaw        string
}

func readEnv() (envDefaults, error) {
	var e envDefaults

	e.env = envString("ENV", "development")
	e.dbDSN = envString("LT_API_DSN", "")
	e.smtpHost = envString("SMTP_HOST", "sandbox.smtp.mailtrap.io")
	e.smtpUsername = envString("SMTP_USERNAME", "")
	e.smtpPassword = envString("SMTP_PASSWORD", "")
	e.smtpSender = envString("SMTP_SENDER", "League Tokens <no-reply@lt.aleksrdvn.com>")
	e.corsRaw = envString("CORS_ORIGINS", "")

	var errs []error
	var err error
	if e.port, err = envInt("PORT", 9000); err != nil {
		errs = append(errs, err)
	}
	if e.dbMaxConns, err = envInt("LT_API_DB_MAX_CONNS", 25); err != nil {
		errs = append(errs, err)
	}
	if e.dbMaxIdleTime, err = envDuration("LT_API_DB_MAX_IDLE_TIME", 15*time.Minute); err != nil {
		errs = append(errs, err)
	}
	if e.limiterRPS, err = envFloat64("LT_API_LIMITER_RPS", 2); err != nil {
		errs = append(errs, err)
	}
	if e.limiterBurst, err = envInt("LT_API_LIMITER_BURST", 4); err != nil {
		errs = append(errs, err)
	}
	if e.limiterEnabled, err = envBool("LT_API_LIMITER_ENABLED", true); err != nil {
		errs = append(errs, err)
	}
	if e.smtpPort, err = envInt("SMTP_PORT", 2525); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return e, errors.Join(errs...)
	}
	return e, nil
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: must be an integer", key, v)
	}
	return n, nil
}

func envFloat64(key string, fallback float64) (float64, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: must be a number", key, v)
	}
	return f, nil
}

func envBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: must be a boolean (true/false)", key, v)
	}
	return b, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: must be a duration (e.g. 15m)", key, v)
	}
	return d, nil
}

// parseCORSOrigins turns the raw CORS_ORIGINS value into an allowlist.
// Origins are canonicalized to lowercase scheme+host (scheme and host are
// case-insensitive in URLs) with any trailing slash stripped, so that exact
// string comparison in the CORS middleware cannot miss on formatting.
// A wildcard is rejected here, not "allow all" later: an allowlist that
// silently degrades into allow-all is worse than a boot failure.
func parseCORSOrigins(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	var origins []string

	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" {
			return nil, fmt.Errorf("wildcard %q is not allowed; list exact origins", part)
		}

		part = strings.TrimSuffix(part, "/")
		u, err := url.Parse(part)
		if err != nil {
			return nil, fmt.Errorf("not a valid URL: %q", part)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("must be a bare origin (scheme://host[:port]), got %q", part)
		}

		part = strings.ToLower(part)
		if _, dup := seen[part]; dup {
			continue
		}

		seen[part] = struct{}{}

		origins = append(origins, part)
	}

	return origins, nil
}

// validate enforces semantic rules that type parsing cannot express. Every
// message names the offending variable — the contract that makes boot
// failures actionable in a deployment log.
func validate(cfg Config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid PORT %d: must be in range 1-65535", cfg.Port)
	}
	switch cfg.Env {
	case "development", "staging", "production":
	default:
		return fmt.Errorf("invalid ENV %q: must be development, staging or production", cfg.Env)
	}
	// Parse, not hand-check: pgx is the authority on what a DSN is. An
	// empty DSN is rejected explicitly — ParseConfig("") would happily
	// fall back to PGHOST/PGUSER-style defaults and blow up later as a
	// connect error instead of a config error.
	if cfg.DB.DSN == "" {
		return fmt.Errorf("invalid LT_API_DSN: is empty; set it in the environment")
	}
	if _, err := pgxpool.ParseConfig(cfg.DB.DSN); err != nil {
		return fmt.Errorf("invalid LT_API_DSN: %w", err)
	}
	if cfg.DB.MaxConns < 1 {
		return fmt.Errorf("invalid LT_API_DB_MAX_CONNS %d: must be at least 1", cfg.DB.MaxConns)
	}
	if cfg.DB.MaxIdleTime <= 0 {
		return fmt.Errorf("invalid LT_API_DB_MAX_IDLE_TIME %s: must be positive", cfg.DB.MaxIdleTime)
	}
	// Validated even when the limiter is disabled: config that is "correct
	// only when something else is off" fails unpredictably when the other
	// thing gets switched on.
	if cfg.Limiter.RPS <= 0 {
		return fmt.Errorf("invalid LT_API_LIMITER_RPS %f: must be positive", cfg.Limiter.RPS)
	}
	if cfg.Limiter.Burst < 1 {
		return fmt.Errorf("invalid LT_API_LIMITER_BURST %d: must be at least 1", cfg.Limiter.Burst)
	}
	return nil
}
