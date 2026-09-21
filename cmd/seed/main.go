package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// seedSQL is the dev-database fixture. Team names come from
// docs/data/euroleague_season.csv; all results are synthetic (seeded RNG, one-off
// Python generator, not kept in the repo). 8 rounds x 5 matches (single
// round-robin minus the last round): every team plays exactly once per round,
// no pairing repeats. Rounds 1-4 are closed (past, fake results); round 5 is
// open (first upcoming); rounds 6-8 are created. 20 players are fake
// registered users of the game (2 favorite fans per team), not real athletes. Deliberately independent of the test
// fixture in internal/api/seed_test.go — dev data and test data serve
// different purposes.
//
//go:embed seed.sql
var seedSQL string

func main() {
	var dsn string
	var reset bool

	// Same precedence as the server (ADR-014): env first, flag overrides.
	flag.StringVar(&dsn, "db-dsn", os.Getenv("LT_API_DSN"), "PostgreSQL DSN (env: LT_API_DSN)")
	flag.BoolVar(&reset, "reset", false, "truncate all tables (RESTART IDENTITY CASCADE) before seeding")
	flag.Parse()

	if dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: seed -db-dsn=<DSN> [-reset]")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(context.Background(), logger, dsn, reset); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, dsn string, reset bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Simple protocol: the seed SQL contains multiple statements, which the
	// default (extended) protocol cannot run in a single Exec.
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if !reset {
		nonEmpty, err := anyTableHasRows(ctx, conn)
		if err != nil {
			return err
		}
		if nonEmpty {
			return fmt.Errorf("database is not empty; re-run with -reset to truncate and reseed")
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if reset {
		if _, err := tx.Exec(ctx, `TRUNCATE matches, players, rounds, seasons, teams RESTART IDENTITY CASCADE`); err != nil {
			return fmt.Errorf("truncate: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, seedSQL); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	logger.Info("seed data applied", "reset", reset)
	return nil
}

// anyTableHasRows reports whether any of the seeded tables contains data. Used
// to refuse seeding a dirty database unless the user asks for a reset.
func anyTableHasRows(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var nonEmpty bool
	err := conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM matches)
		    OR EXISTS (SELECT 1 FROM players)
		    OR EXISTS (SELECT 1 FROM rounds)
		    OR EXISTS (SELECT 1 FROM seasons)
		    OR EXISTS (SELECT 1 FROM teams)
	`).Scan(&nonEmpty)
	if err != nil {
		return false, fmt.Errorf("check for existing rows: %w", err)
	}
	return nonEmpty, nil
}
