package config

import (
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
	return cfg
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // substring; empty means must pass
	}{
		{
			name:   "baseline passes",
			mutate: func(*Config) {},
		},
		{
			name:    "port below range",
			mutate:  func(c *Config) { c.Port = 0 },
			wantErr: "PORT",
		},
		{
			name:    "port above range",
			mutate:  func(c *Config) { c.Port = 65536 },
			wantErr: "PORT",
		},
		{
			name:    "unknown environment",
			mutate:  func(c *Config) { c.Env = "prod" },
			wantErr: "ENV",
		},
		{
			name:    "empty DSN",
			mutate:  func(c *Config) { c.DB.DSN = "" },
			wantErr: "LT_API_DSN",
		},
		{
			name:    "unparseable DSN (bad sslmode)",
			mutate:  func(c *Config) { c.DB.DSN = "postgres://user@localhost/db?sslmode=bogus" },
			wantErr: "LT_API_DSN",
		},
		{
			name:    "zero max conns",
			mutate:  func(c *Config) { c.DB.MaxConns = 0 },
			wantErr: "LT_API_DB_MAX_CONNS",
		},
		{
			name:    "zero max idle time",
			mutate:  func(c *Config) { c.DB.MaxIdleTime = 0 },
			wantErr: "LT_API_DB_MAX_IDLE_TIME",
		},
		{
			name:    "zero limiter rps",
			mutate:  func(c *Config) { c.Limiter.RPS = 0 },
			wantErr: "LT_API_LIMITER_RPS",
		},
		{
			name:    "zero limiter burst",
			mutate:  func(c *Config) { c.Limiter.Burst = 0 },
			wantErr: "LT_API_LIMITER_BURST",
		},
		{
			name:    "limiter disabled does not excuse bad values",
			mutate:  func(c *Config) { c.Limiter.Enabled = false; c.Limiter.Burst = 0 },
			wantErr: "LT_API_LIMITER_BURST",
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
			// Every message names the offending variable — that is the
			// contract that makes boot failures actionable.
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() error %q does not name %q", err, tt.wantErr)
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
			name:    "no env, no args: DSN validation still fires inside Parse",
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
