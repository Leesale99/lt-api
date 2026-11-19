package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// validConfig is the baseline every case in TestValidate mutates. If it ever
// stops passing validate, that is a regression in the baseline itself, not
// in the cases — which is exactly why it exists.
func validConfig() Config {
	var cfg Config
	cfg.Port = 9000
	cfg.Env = "development"
	cfg.DB.DSN = "postgres://lt:test@localhost:5432/lt_test?sslmode=disable"
	cfg.DB.MaxConns = 25
	cfg.DB.MaxIdleTime = 15 * time.Minute
	cfg.Limiter.RPS = 2
	cfg.Limiter.Burst = 4
	cfg.Limiter.Enabled = true
	cfg.SMTP.Port = 2525
	cfg.SMTP.Sender = "League Tokens <no-reply@lt.aleksrdvn.com>"
	return cfg
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		// mutate breaks exactly one setting.
		mutate func(*Config)
		// wantErr is the setting's env spelling; wantFlag is its flag
		// spelling. Every refusal must name both (misalignment-report §6
		// item 17: stdlib flag cannot say whether a typed value was passed
		// on the command line, so the message names both knobs).
		wantErr  string
		wantFlag string
		wantMsg  string // exact message, when the wording itself is the pin
	}{
		{
			name:   "baseline passes",
			mutate: func(*Config) {},
		},
		{
			name:     "port below range",
			mutate:   func(c *Config) { c.Port = 0 },
			wantErr:  "PORT",
			wantFlag: "-port",
			wantMsg:  "invalid PORT 0: must be in range 1-65535 (env PORT or -port flag)",
		},
		{
			name:     "port above range",
			mutate:   func(c *Config) { c.Port = 65536 },
			wantErr:  "PORT",
			wantFlag: "-port",
		},
		{
			name:     "unknown environment",
			mutate:   func(c *Config) { c.Env = "prod" },
			wantErr:  "ENV",
			wantFlag: "-env",
		},
		{
			name:     "empty DSN",
			mutate:   func(c *Config) { c.DB.DSN = "" },
			wantErr:  "LT_API_DSN",
			wantFlag: "-db-dsn",
		},
		{
			name:     "unparseable DSN (bad sslmode)",
			mutate:   func(c *Config) { c.DB.DSN = "postgres://user@localhost/db?sslmode=bogus" },
			wantErr:  "LT_API_DSN",
			wantFlag: "-db-dsn",
		},
		{
			name:     "unparseable per-context DSN",
			mutate:   func(c *Config) { c.DB.DSNGame = "postgres://user@localhost/db?sslmode=bogus" },
			wantErr:  "LT_API_DSN_GAME",
			wantFlag: "-db-dsn-game",
		},
		{
			name:     "zero max conns",
			mutate:   func(c *Config) { c.DB.MaxConns = 0 },
			wantErr:  "LT_API_DB_MAX_CONNS",
			wantFlag: "-db-max-conns",
		},
		{
			name:     "zero max idle time",
			mutate:   func(c *Config) { c.DB.MaxIdleTime = 0 },
			wantErr:  "LT_API_DB_MAX_IDLE_TIME",
			wantFlag: "-db-max-idle-time",
		},
		{
			name:     "zero limiter rps",
			mutate:   func(c *Config) { c.Limiter.RPS = 0 },
			wantErr:  "LT_API_LIMITER_RPS",
			wantFlag: "-limiter-rps",
		},
		{
			name:     "zero limiter burst",
			mutate:   func(c *Config) { c.Limiter.Burst = 0 },
			wantErr:  "LT_API_LIMITER_BURST",
			wantFlag: "-limiter-burst",
		},
		{
			name:     "limiter disabled does not excuse bad values",
			mutate:   func(c *Config) { c.Limiter.Enabled = false; c.Limiter.Burst = 0 },
			wantErr:  "LT_API_LIMITER_BURST",
			wantFlag: "-limiter-burst",
		},
		{
			name:     "smtp port below range",
			mutate:   func(c *Config) { c.SMTP.Port = 0 },
			wantErr:  "SMTP_PORT",
			wantFlag: "-smtp-port",
			wantMsg:  "invalid SMTP_PORT 0: must be in range 1-65535 (env SMTP_PORT or -smtp-port flag)",
		},
		{
			name:     "smtp port above range",
			mutate:   func(c *Config) { c.SMTP.Port = 65536 },
			wantErr:  "SMTP_PORT",
			wantFlag: "-smtp-port",
		},
		{
			name:     "smtp port negative",
			mutate:   func(c *Config) { c.SMTP.Port = -25 },
			wantErr:  "SMTP_PORT",
			wantFlag: "-smtp-port",
		},
		{
			name:     "smtp sender is not an address",
			mutate:   func(c *Config) { c.SMTP.Sender = "no-reply" },
			wantErr:  "SMTP_SENDER",
			wantFlag: "-smtp-sender",
		},
		{
			name:     "smtp sender is empty",
			mutate:   func(c *Config) { c.SMTP.Sender = "" },
			wantErr:  "SMTP_SENDER",
			wantFlag: "-smtp-sender",
			wantMsg:  `invalid SMTP_SENDER "": must be provided (env SMTP_SENDER or -smtp-sender flag)`,
		},
		{
			// The default sender carries a display name: the check is a mailbox
			// parse, not identity's bare-address EmailRX.
			name:   "smtp sender with a display name passes",
			mutate: func(c *Config) { c.SMTP.Sender = "League Tokens <no-reply@lt.aleksrdvn.com>" },
		},
		{
			name:   "smtp sender as a bare address passes",
			mutate: func(c *Config) { c.SMTP.Sender = "no-reply@lt.aleksrdvn.com" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := validate(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want error containing %q", tt.wantErr)
			}
			// Every message names the offending setting by BOTH spellings —
			// that is the contract that makes boot failures actionable.
			for _, want := range []string{tt.wantErr, tt.wantFlag, "flag"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("validate() error %q does not name %q", err, want)
				}
			}
			if tt.wantMsg != "" && err.Error() != tt.wantMsg {
				t.Errorf("validate() error = %q, want %q", err, tt.wantMsg)
			}
		})
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr string
	}{
		{
			name: "empty raw is nil (disabled)",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace-only raw is nil (disabled)",
			raw:  "  ,  ",
			want: nil,
		},
		{
			name: "single origin",
			raw:  "https://lt.aleksrdvn.com",
			want: []string{"https://lt.aleksrdvn.com"},
		},
		{
			name: "multiple origins, spaces and empties tolerated",
			raw:  " https://a.example , , https://b.example ,",
			want: []string{"https://a.example", "https://b.example"},
		},
		{
			name: "case and trailing slash canonicalized, duplicates dropped",
			raw:  "HTTPS://A.Example/, https://a.example",
			want: []string{"https://a.example"},
		},
		{
			name:    "wildcard rejected",
			raw:     "*",
			wantErr: "wildcard",
		},
		{
			name:    "path is not an origin",
			raw:     "https://a.example/teams",
			wantErr: "bare origin",
		},
		{
			name:    "missing scheme",
			raw:     "a.example",
			wantErr: "bare origin",
		},
		{
			name:    "non-http scheme",
			raw:     "ftp://a.example",
			wantErr: "bare origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCORSOrigins(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseCORSOrigins(%q) = nil error, want error containing %q", tt.raw, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCORSOrigins(%q) = %v, want nil", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("origin[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParse(t *testing.T) {
	const validDSN = "postgres://lt:test@localhost:5432/lt_test?sslmode=disable"

	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		check   func(t *testing.T, cfg Config)
		wantErr string // substring; empty means must pass
	}{
		{
			// LT_API_DSN is pinned to "" (not just absent): .envrc feeds a
			// real DSN into every make recipe, so relying on the ambient
			// environment makes this case environment-dependent.
			name:    "no env, no args: DSN validation still fires inside Parse",
			env:     map[string]string{"LT_API_DSN": ""},
			wantErr: "LT_API_DSN",
		},
		{
			name: "env provides config",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"PORT":       "9100",
				"ENV":        "staging",
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.Port != 9100 || cfg.Env != "staging" {
					t.Errorf("got Port=%d Env=%q, want 9100/staging", cfg.Port, cfg.Env)
				}
			},
		},
		{
			name: "LOG_LEVEL parses case-insensitively",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"LOG_LEVEL":  "DEBUG",
			},
			check: func(t *testing.T, cfg Config) {
				if cfg.LogLevel != slog.LevelDebug {
					t.Errorf("got LogLevel=%v, want DEBUG", cfg.LogLevel)
				}
			},
		},
		{
			name: "bad env LOG_LEVEL fails at boot, naming the variable",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"LOG_LEVEL":  "vervose",
			},
			wantErr: "LOG_LEVEL",
		},
		{
			name: "flag overrides env log level and a bad flag value names the flag",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"LOG_LEVEL":  "warn",
			},
			args: []string{"-log-level=error"},
			check: func(t *testing.T, cfg Config) {
				if cfg.LogLevel != slog.LevelError {
					t.Errorf("got LogLevel=%v, want ERROR", cfg.LogLevel)
				}
			},
		},
		{
			name:    "bad flag log level names the flag, not the env var",
			env:     map[string]string{"LT_API_DSN": validDSN, "LOG_LEVEL": "warn"},
			args:    []string{"-log-level=vervose"},
			wantErr: "-log-level",
		},
		{
			name: "flag overrides env (precedence flag > env > default)",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"PORT":       "9100",
			},
			args: []string{"-port=7000"},
			check: func(t *testing.T, cfg Config) {
				if cfg.Port != 7000 {
					t.Errorf("got Port=%d, want 7000", cfg.Port)
				}
			},
		},
		{
			// The value arrived as a flag, not a variable: the message must
			// name the flag too, or the operator is told to change something
			// they never set (misalignment-report §6 item 17).
			name:    "bad flag port names both spellings",
			env:     map[string]string{"LT_API_DSN": validDSN},
			args:    []string{"-port=0"},
			wantErr: "env PORT or -port flag",
		},
		{
			name: "a malformed SMTP_SENDER fails at boot, naming both spellings",
			env: map[string]string{
				"LT_API_DSN":    validDSN,
				"SMTP_SENDER":   "not-an-address",
				"SMTP_USERNAME": "user",
				"SMTP_PASSWORD": "pass",
			},
			wantErr: "env SMTP_SENDER or -smtp-sender flag",
		},
		{
			name: "an out-of-range SMTP_PORT fails at boot",
			env: map[string]string{
				"LT_API_DSN":  validDSN,
				"SMTP_PORT":   "0",
				"SMTP_SENDER": "no-reply@lt.aleksrdvn.com",
			},
			wantErr: "invalid SMTP_PORT 0: must be in range 1-65535",
		},
		{
			name: "the default sender with a display name boots",
			env: map[string]string{
				"LT_API_DSN":  validDSN,
				"SMTP_SENDER": "League Tokens <no-reply@lt.aleksrdvn.com>",
			},
		},
		{
			name: "untyped env value fails at boot, naming the variable",
			env: map[string]string{
				"LT_API_DSN": validDSN,
				"PORT":       "not-a-number",
			},
			wantErr: "PORT",
		},
		{
			name: "several bad env vars are reported together",
			env: map[string]string{
				"LT_API_DSN":           validDSN,
				"PORT":                 "not-a-number",
				"LT_API_LIMITER_BURST": "not-a-number",
			},
			wantErr: "LT_API_LIMITER_BURST",
		},
		{
			name:    "unknown flag is an error, not an exit",
			env:     map[string]string{"LT_API_DSN": validDSN},
			args:    []string{"-nope"},
			check:   func(t *testing.T, cfg Config) {},
			wantErr: "nope",
		},
		{
			name: "CORS_ORIGINS flows through Parse",
			env: map[string]string{
				"LT_API_DSN":   validDSN,
				"CORS_ORIGINS": "HTTPS://A.Example/, https://b.example",
			},
			check: func(t *testing.T, cfg Config) {
				want := []string{"https://a.example", "https://b.example"}
				if len(cfg.CORS.Origins) != len(want) {
					t.Fatalf("got %v, want %v", cfg.CORS.Origins, want)
				}
				for i := range want {
					if cfg.CORS.Origins[i] != want[i] {
						t.Errorf("origin[%d] = %q, want %q", i, cfg.CORS.Origins[i], want[i])
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := Parse(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse() = nil error, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() = %v, want nil", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
