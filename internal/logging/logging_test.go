package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"
)

// captureLogger builds a logger against a buffer instead of stdout, so the
// records can be asserted on directly.
func captureLogger(t *testing.T, level slog.Level, env string) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := newLogger(level, env, &buf)
	return logger, &buf
}

func TestNewJSONOutput(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelInfo, "production")

	logger.Info("database connection pool established")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("record is not a JSON object: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "database connection pool established" {
		t.Errorf("got msg=%v", rec["msg"])
	}
	if rec["level"] != "INFO" {
		t.Errorf("got level=%v, want INFO", rec["level"])
	}
	// Machine format: no source bulk outside development.
	if _, present := rec["source"]; present {
		t.Error("production record must not carry a source attribute")
	}
}

func TestNewLevelFiltering(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelWarn, "production")

	logger.Info("below the threshold")
	logger.Warn("at the threshold")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1 (only the warn)", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if rec["msg"] != "at the threshold" {
		t.Errorf("got msg=%v", rec["msg"])
	}
}

func TestNewSourceShortFileInDevelopment(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelInfo, "development")

	logger.Info("from somewhere")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	src, ok := rec["source"].(map[string]any)
	if !ok {
		t.Fatalf("no source attribute: %v", rec)
	}
	file, _ := src["file"].(string)
	// Short form "<dir>/<file>:<line>", never an absolute path — an absolute
	// path would leak the build machine's checkout location into every record.
	if !strings.Contains(file, "/") || strings.HasPrefix(file, "/") {
		t.Errorf("source file %q is not a short relative path", file)
	}
	if file != "logging_test.go" && !strings.HasSuffix(file, "logging_test.go") {
		t.Errorf("source file %q does not name this file", file)
	}
}

func TestPgxLevelMapping(t *testing.T) {
	tests := []struct {
		pgx     tracelog.LogLevel
		wantLvl string // JSONHandler's level name; "" means no record expected
	}{
		{tracelog.LogLevelError, "ERROR"},
		{tracelog.LogLevelWarn, "WARN"},
		{tracelog.LogLevelInfo, "INFO"},
		{tracelog.LogLevelDebug, "DEBUG"},
		{tracelog.LogLevelTrace, "DEBUG"},
		{tracelog.LogLevelNone, ""}, // nothing meaningful to say at "none"
	}

	for _, tt := range tests {
		logger, buf := captureLogger(t, slog.LevelDebug, "production")
		Pgx(logger).Log(context.Background(), tt.pgx, "pgx event", nil)
		out := strings.TrimSpace(buf.String())
		if tt.wantLvl == "" {
			if out != "" {
				t.Errorf("LogLevel %v produced a record, want none: %s", tt.pgx, out)
			}
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(out), &rec); err != nil {
			t.Fatalf("LogLevel %v: record is not JSON: %v", tt.pgx, err)
		}
		if rec["level"] != tt.wantLvl {
			t.Errorf("LogLevel %v: got level=%v, want %s", tt.pgx, rec["level"], tt.wantLvl)
		}
		if rec["msg"] != "pgx event" {
			t.Errorf("LogLevel %v: got msg=%v", tt.pgx, rec["msg"])
		}
	}
}

func TestPgxDataBecomesSortedAttributes(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelInfo, "production")

	Pgx(logger).Log(context.Background(), tracelog.LogLevelWarn, "slow query",
		map[string]any{"time": 300, "pid": 42, "sql": "SELECT 1"})

	raw := buf.String()
	// Deterministic key order: the JSON must be byte-identical across runs,
	// which map iteration order alone does not guarantee.
	const want = `"pid":42,"sql":"SELECT 1","time":300`
	if !strings.Contains(raw, want) {
		t.Errorf("attributes not sorted as %q in: %s", want, raw)
	}
}
