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
	"log/slog"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Version  string
	Port     int
	Env      string
	LogLevel slog.Level
	DB       struct {
		DSN         string
		MaxConns    int
		MaxIdleTime time.Duration
		// Per-context DSN overrides: each connection pool is
		// bound to one bounded context (schedule/game/identity) and runs
		// SET ROLE <context_role> on every connection. Derived from DSN
		// unless the override is set (deployment splits the contexts onto
		// separate databases/users). The names are context names, not
		// domain logic — config stays domain-agnostic.
		DSNSchedule string
		DSNGame     string
		DSNIdentity string
	}
	Limiter struct {
		RPS     float64
		Burst   int
		Enabled bool
		// TrustedProxies is the reverse-proxy allowlist: a peer inside one of
		// these prefixes may name the client in X-Forwarded-For, and the
		// limiter keys buckets on that client instead of on the proxy. Empty
		// (the default) trusts no peer, so X-Forwarded-For is ignored and the
		// bucket key is the connection address — the safe default, since an
		// untrusted caller writes that header itself.
		TrustedProxies []netip.Prefix
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
// rule:R-PLATFORM-001
// rule:R-PLATFORM-002
func Parse(args []string) (Config, error) {
	version := buildVersion()
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

	// Level parsing happens after fs.Parse, not in a typed flag var: the
	// error message must name the variable the bad value came from (flag or
	// env), and slog.Level has no flag.Value implementation to attach it to.
	logLevelRaw := env.logLevel
	fs.StringVar(&logLevelRaw, "log-level", env.logLevel, "Log level (debug|info|warn|error) (env: LOG_LEVEL)")

	fs.StringVar(&cfg.DB.DSN, "db-dsn", env.dbDSN, "PostgreSQL DSN (env: LT_API_DSN)")
	fs.StringVar(&cfg.DB.DSNSchedule, "db-dsn-schedule", env.dbDSNSchedule, "Schedule-context PostgreSQL DSN override (env: LT_API_DSN_SCHEDULE)")
	fs.StringVar(&cfg.DB.DSNGame, "db-dsn-game", env.dbDSNGame, "Game-context PostgreSQL DSN override (env: LT_API_DSN_GAME)")
	fs.StringVar(&cfg.DB.DSNIdentity, "db-dsn-identity", env.dbDSNIdentity, "Identity-context PostgreSQL DSN override (env: LT_API_DSN_IDENTITY)")
	fs.IntVar(&cfg.DB.MaxConns, "db-max-conns", env.dbMaxConns, "PostgreSQL max open and idle connections PER CONTEXT POOL (env: LT_API_DB_MAX_CONNS)")
	fs.DurationVar(&cfg.DB.MaxIdleTime, "db-max-idle-time", env.dbMaxIdleTime, "PostgreSQL max connection idle time (env: LT_API_DB_MAX_IDLE_TIME)")

	fs.Float64Var(&cfg.Limiter.RPS, "limiter-rps", env.limiterRPS, "Rate limiter maximum requests per second (env: LT_API_LIMITER_RPS)")
	fs.IntVar(&cfg.Limiter.Burst, "limiter-burst", env.limiterBurst, "Rate limiter maximum burst (env: LT_API_LIMITER_BURST)")
	fs.BoolVar(&cfg.Limiter.Enabled, "limiter-enabled", env.limiterEnabled, "Enable rate limiter (env: LT_API_LIMITER_ENABLED)")

	// Parsed after fs.Parse, for the same reason LOG_LEVEL is: a bare IP has
	// to become a prefix, and the refusal must name the knob the bad value
	// came from.
	trustedProxiesRaw := env.trustedProxiesRaw
	fs.StringVar(&trustedProxiesRaw, "trusted-proxies", trustedProxiesRaw, "Reverse proxies whose X-Forwarded-For is trusted, as comma-separated IPs/CIDRs; empty trusts none (env: TRUSTED_PROXIES)")

	fs.StringVar(&cfg.SMTP.Host, "smtp-host", env.smtpHost, "SMTP host (env: SMTP_HOST)")
	fs.IntVar(&cfg.SMTP.Port, "smtp-port", env.smtpPort, "SMTP port (env: SMTP_PORT)")
	fs.StringVar(&cfg.SMTP.Username, "smtp-username", env.smtpUsername, "SMTP username (env: SMTP_USERNAME)")
	fs.StringVar(&cfg.SMTP.Password, "smtp-password", env.smtpPassword, "SMTP password (env: SMTP_PASSWORD)")
	fs.StringVar(&cfg.SMTP.Sender, "smtp-sender", env.smtpSender, "SMTP sender (env: SMTP_SENDER)")

	displayVersion := fs.Bool("version", false, "Display version and exit")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if *displayVersion {
		fmt.Printf("Version: \t%s\n", version)
		os.Exit(0)
	}

	// No flag for Version
	cfg.Version = version

	// No flag for CORS_ORIGINS: an origin allowlist is deployment data, not
	// an operator override — there is no legitimate reason to inject it per-run.
	cfg.CORS.Origins, err = parseCORSOrigins(env.corsRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid CORS_ORIGINS: %w", err)
	}

	cfg.LogLevel, err = parseLogLevel(logLevelRaw, "-log-level")
	if err != nil {
		return Config{}, err
	}

	cfg.Limiter.TrustedProxies, err = parseTrustedProxies(trustedProxiesRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid TRUSTED_PROXIES: %w (%s)", err, knob("TRUSTED_PROXIES", "-trusted-proxies"))
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
	port              int
	env               string
	logLevel          string
	dbDSN             string
	dbDSNSchedule     string
	dbDSNGame         string
	dbDSNIdentity     string
	dbMaxConns        int
	dbMaxIdleTime     time.Duration
	limiterRPS        float64
	limiterBurst      int
	limiterEnabled    bool
	trustedProxiesRaw string
	smtpHost          string
	smtpPort          int
	smtpUsername      string
	smtpPassword      string
	smtpSender        string
	corsRaw           string
}

// rule:R-PLATFORM-001
// rule:R-PLATFORM-002
// rule:R-PLATFORM-004
func readEnv() (envDefaults, error) {
	var e envDefaults

	e.env = envString("ENV", "development")
	e.logLevel = envString("LOG_LEVEL", "info")
	e.dbDSN = envString("LT_API_DSN", "")
	e.dbDSNSchedule = envString("LT_API_DSN_SCHEDULE", "")
	e.dbDSNGame = envString("LT_API_DSN_GAME", "")
	e.dbDSNIdentity = envString("LT_API_DSN_IDENTITY", "")
	e.smtpHost = envString("SMTP_HOST", "sandbox.smtp.mailtrap.io")
	e.smtpUsername = envString("SMTP_USERNAME", "")
	e.smtpPassword = envString("SMTP_PASSWORD", "")
	e.smtpSender = envString("SMTP_SENDER", "League Tokens <no-reply@lt.aleksrdvn.com>")
	e.corsRaw = envString("CORS_ORIGINS", "")
	e.trustedProxiesRaw = envString("TRUSTED_PROXIES", "")

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
	if _, err = parseLogLevel(e.logLevel, "LOG_LEVEL"); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return e, errors.Join(errs...)
	}
	return e, nil
}

// parseLogLevel converts a LOG_LEVEL / -log-level value into a slog level.
// slog.Level.UnmarshalText is the authority: case-insensitive debug|info|
// warn|error (plus numeric offsets, e.g. "INFO+2"), and an unknown name is
// an error — the zero value is INFO, so a silent fallback would quietly
// flood or silence a deployment whose level was mistyped.
//
// source names the variable the value came from ("LOG_LEVEL" from the env,
// "-log-level" from args): env values are validated in readEnv before flags
// ever parse, so a failure here can only originate from the flag, and the
// name is still reported — the contract is a message naming the variable.
// rule:R-PLATFORM-003
// rule:R-PLATFORM-004
func parseLogLevel(raw, source string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("invalid %s %q: must be debug, info, warn or error", source, raw)
	}
	return level, nil
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

// parseTrustedProxies turns the raw TRUSTED_PROXIES value into the reverse
// proxy allowlist. Each entry must be an IP prefix; a bare IP is accepted and
// means exactly that ONE host — /32 for IPv4, /128 for IPv6 — never a whole
// /8, because an operator who typed a single address named a single proxy.
// Prefixes are masked, so host bits in 10.1.2.3/8 cannot make the membership
// test surprising. Empty (or whitespace/commas only) yields nil: nobody is
// trusted, which is the pre-proxy behaviour.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix

	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(part); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}

		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP or CIDR prefix (a bare IP means that single host: /32 or /128)", part)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}

	return prefixes, nil
}

// parseCORSOrigins turns the raw CORS_ORIGINS value into an allowlist.
// Origins are canonicalized to lowercase scheme+host (scheme and host are
// case-insensitive in URLs) with any trailing slash stripped, so that exact
// string comparison in the CORS middleware cannot miss on formatting.
// A wildcard is rejected here, not "allow all" later: an allowlist that
// silently degrades into allow-all is worse than a boot failure.
// rule:R-PLATFORM-005
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

// knob names one setting by BOTH spellings an operator can set it with: the
// environment variable and the flag. validate cannot know which of the two
// supplied the value — the stdlib flag package exposes no "was this flag
// passed" for a typed var — so every refusal names both. A message that named
// only the env var told an operator who ran with -port to go change a variable
// they never set (misalignment-report §6 item 17).
func knob(env, flag string) string {
	return fmt.Sprintf("env %s or %s flag", env, flag)
}

// validate enforces semantic rules that type parsing cannot express. Every
// message names the offending setting by both spellings — the contract that
// makes boot failures actionable in a deployment log.
// rule:R-PLATFORM-003
func validate(cfg Config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid PORT %d: must be in range 1-65535 (%s)", cfg.Port, knob("PORT", "-port"))
	}
	switch cfg.Env {
	case "development", "staging", "production":
	default:
		return fmt.Errorf("invalid ENV %q: must be development, staging or production (%s)", cfg.Env, knob("ENV", "-env"))
	}
	// Parse, not hand-check: pgx is the authority on what a DSN is. An
	// empty DSN is rejected explicitly — ParseConfig("") would happily
	// fall back to PGHOST/PGUSER-style defaults and blow up later as a
	// connect error instead of a config error.
	if cfg.DB.DSN == "" {
		return fmt.Errorf("invalid LT_API_DSN: is empty; set it in the environment or pass %s", knob("LT_API_DSN", "-db-dsn"))
	}
	if _, err := pgxpool.ParseConfig(cfg.DB.DSN); err != nil {
		return fmt.Errorf("invalid LT_API_DSN (%s): %w", knob("LT_API_DSN", "-db-dsn"), err)
	}
	// The per-context overrides must each be a DSN when present: a typo in
	// one of them must fail at boot with the setting named, not at the
	// first acquire.
	for _, override := range []struct {
		env, flag, dsn string
	}{
		{"LT_API_DSN_SCHEDULE", "-db-dsn-schedule", cfg.DB.DSNSchedule},
		{"LT_API_DSN_GAME", "-db-dsn-game", cfg.DB.DSNGame},
		{"LT_API_DSN_IDENTITY", "-db-dsn-identity", cfg.DB.DSNIdentity},
	} {
		if override.dsn == "" {
			continue
		}
		if _, err := pgxpool.ParseConfig(override.dsn); err != nil {
			return fmt.Errorf("invalid %s (%s): %w", override.env, knob(override.env, override.flag), err)
		}
	}
	if cfg.DB.MaxConns < 1 {
		return fmt.Errorf("invalid LT_API_DB_MAX_CONNS %d: must be at least 1 (%s)", cfg.DB.MaxConns, knob("LT_API_DB_MAX_CONNS", "-db-max-conns"))
	}
	if cfg.DB.MaxIdleTime <= 0 {
		return fmt.Errorf("invalid LT_API_DB_MAX_IDLE_TIME %s: must be positive (%s)", cfg.DB.MaxIdleTime, knob("LT_API_DB_MAX_IDLE_TIME", "-db-max-idle-time"))
	}
	// Validated even when the limiter is disabled: config that is "correct
	// only when something else is off" fails unpredictably when the other
	// thing gets switched on.
	if cfg.Limiter.RPS <= 0 {
		return fmt.Errorf("invalid LT_API_LIMITER_RPS %f: must be positive (%s)", cfg.Limiter.RPS, knob("LT_API_LIMITER_RPS", "-limiter-rps"))
	}
	if cfg.Limiter.Burst < 1 {
		return fmt.Errorf("invalid LT_API_LIMITER_BURST %d: must be at least 1 (%s)", cfg.Limiter.Burst, knob("LT_API_LIMITER_BURST", "-limiter-burst"))
	}
	// The mail settings are judged here for the same reason every other
	// setting is: a port outside 1-65535 or a sender that is not an address
	// must fail at boot, not at the first send (misalignment-report §6 item
	// 16). An unusable pair that reached the mailer would accept requests the
	// process cannot honour (R-PLATFORM-006).
	if cfg.SMTP.Port < 1 || cfg.SMTP.Port > 65535 {
		return fmt.Errorf("invalid SMTP_PORT %d: must be in range 1-65535 (%s)", cfg.SMTP.Port, knob("SMTP_PORT", "-smtp-port"))
	}
	if err := validateSender(cfg.SMTP.Sender); err != nil {
		return fmt.Errorf("invalid SMTP_SENDER %q: %w (%s)", cfg.SMTP.Sender, err, knob("SMTP_SENDER", "-smtp-sender"))
	}
	return nil
}

// validateSender checks the outbound sender as an RFC 5322 mailbox. net/mail is
// the authority here, not kernel/validator's EmailRX: the sender may carry a
// display name — the built-in default is "League Tokens <no-reply@…>" — which
// EmailRX (the account-address rule identity's registration uses) deliberately
// refuses. A sender that is not a mailbox is refused at boot so the first
// delivery is never the first report of a configuration mistake.
func validateSender(sender string) error {
	if sender == "" {
		return errors.New("must be provided")
	}
	if _, err := mail.ParseAddress(sender); err != nil {
		return errors.New("must be an email address")
	}
	return nil
}
