// Package logging owns the construction of the process-wide slog.Logger and
// the bridge that lets pgx emit through it. Building the logger in exactly
// one place keeps the handler (and therefore the output format) a startup
// decision: call sites only ever see *slog.Logger and stay format-agnostic.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/tracelog"
)
// New returns the application logger: JSON records on stdout, at the given
// level. Source call sites (short file:line) are attached in development
// only — production aggregates parse machine data, and a full source path
// in every record is bulk without payoff there.
func New(level slog.Level, env string) *slog.Logger {
	return newLogger(level, env, os.Stdout)
}

func newLogger(level slog.Level, env string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
		// Source is a dev aid: it answers "which line logged this" while
		// reading a terminal, the one place a human parses records.
		AddSource: env == "development",
	}
	if opts.AddSource {
		opts.ReplaceAttr = shortSource
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// shortSource rewrites the source attribute from a full path to
// "<dir>/<file>:<line>". The extra directory keeps same-named files apart
// (api/server.go vs config/server.go) while staying readable.
func shortSource(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.SourceKey {
		if src, ok := a.Value.Any().(*slog.Source); ok {
			src.File = filepath.Join(filepath.Base(filepath.Dir(src.File)), filepath.Base(src.File))
		}
	}
	return a
}
// Pgx adapts the application logger to pgx's tracelog.Logger interface, so
// database connection and query events land in the same JSON stream as
// application logs — one log pipeline per process, not two.
func Pgx(logger *slog.Logger) tracelog.Logger {
	return pgxLogger{logger: logger}
}

type pgxLogger struct {
	logger *slog.Logger
}

func (p pgxLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	var lvl slog.Level
	switch level {
	case tracelog.LogLevelError:
		lvl = slog.LevelError
	case tracelog.LogLevelWarn:
		lvl = slog.LevelWarn
	case tracelog.LogLevelInfo:
		lvl = slog.LevelInfo
	case tracelog.LogLevelDebug, tracelog.LogLevelTrace:
		lvl = slog.LevelDebug
	default: // LogLevelNone and anything unknown: nothing meaningful to say
		return
	}

	// Sorted keys: JSONHandler would otherwise print map iteration order,
	// which makes two records of the same event non-comparable.
	attrs := make([]slog.Attr, 0, len(data))
	for _, key := range sortedKeys(data) {
		attrs = append(attrs, slog.Any(key, data[key]))
	}
	p.logger.LogAttrs(ctx, lvl, msg, attrs...)
}

func sortedKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
