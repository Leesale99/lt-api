// Package repository holds the schedule context's storage layer: one
// repository per entity plus the narrow game-facing surface (game_facing.go).
// Straightforward store SQL lives here — no rules beyond what the statements
// and their error classification encode — and nothing is published: the
// post-commit events belong to the service layer, which owns the transaction
// boundary and the bus.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

type MatchRepository struct {
	pool *pgxpool.Pool
}

func NewMatchRepository(pool *pgxpool.Pool) *MatchRepository {
	return &MatchRepository{pool: pool}
}

func (r *MatchRepository) Get(ctx context.Context, id int) (model.Match, error) {
	if id < 1 {
		return model.Match{}, persistence.ErrRecordNotFound
	}

	query := `
		SELECT  id, created_at, starts_at, ended_at, season_id, round_id, home_team_id, away_team_id, status, home_odds, away_odds, home_score, away_score, version
		FROM matches
		WHERE id = $1
	`

	var match model.Match

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&match.ID,
		&match.CreatedAt,
		&match.StartsAt,
		&match.EndedAt,
		&match.SeasonID,
		&match.RoundID,
		&match.HomeTeamID,
		&match.AwayTeamID,
		&match.Status,
		&match.Odds.Home,
		&match.Odds.Away,
		&match.Score.Home,
		&match.Score.Away,
		&match.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Match{}, persistence.ErrRecordNotFound
		default:
			return model.Match{}, err
		}
	}

	return match, nil
}

func (r *MatchRepository) Insert(ctx context.Context, match model.Match) (model.Match, error) {
	query := `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, starts_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, ended_at, status, version
	`
	args := []any{
		match.SeasonID,
		match.RoundID,
		match.HomeTeamID,
		match.AwayTeamID,
		match.Odds.Home,
		match.Odds.Away,
		match.Score.Home,
		match.Score.Away,
		match.StartsAt,
	}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&match.ID, &match.CreatedAt, &match.EndedAt, &match.Status, &match.Version)

	return match, err
}

func (r *MatchRepository) GetAll(ctx context.Context, seasonID, roundID int, status model.MatchStatus, filters persistence.Filters) ([]model.Match, persistence.Metadata, error) {
	// Optional filters are composed in Go rather than OR-ed into a cached
	// statement: [[ADR-006 - Conditional WHERE for optional filters (never OR $1 = '')]].
	conds, args := []string{}, []any{}

	if seasonID != 0 {
		args = append(args, seasonID)
		conds = append(conds, fmt.Sprintf("season_id = $%d", len(args)))
	}
	if roundID != 0 {
		args = append(args, roundID)
		conds = append(conds, fmt.Sprintf("round_id = $%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, starts_at, ended_at, season_id, round_id, home_team_id, away_team_id, status, home_odds, away_odds, home_score, away_score, version
		FROM matches%s
		ORDER BY %s %s, id ASC
		LIMIT $%d OFFSET $%d
	`, where, filters.SortColumn(), filters.SortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.Limit(), filters.Offset())

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, persistence.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	matches := []model.Match{}

	for rows.Next() {
		var match model.Match
		err := rows.Scan(
			&totalRecords,
			&match.ID,
			&match.CreatedAt,
			&match.StartsAt,
			&match.EndedAt,
			&match.SeasonID,
			&match.RoundID,
			&match.HomeTeamID,
			&match.AwayTeamID,
			&match.Status,
			&match.Odds.Home,
			&match.Odds.Away,
			&match.Score.Home,
			&match.Score.Away,
			&match.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		matches = append(matches, match)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return matches, metadata, nil
}

func (r *MatchRepository) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM matches
		WHERE id = $1 AND season_id = $2
	`

	result, err := r.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		// rides_match_id_fkey — the FK that made this refusal
		// automatic — is gone with the cross-context FKs (000016); the
		// "match still has rides" refusal moved to the delete policy
		// (service.DeleteMatch), which asks the game context before deleting
		// (same 409). The remaining FK refusals are schedule-internal.
		if persistence.IsDeleteRestricted(err) {
			return persistence.ErrRecordInUse
		}
		return err
	}

	if result.RowsAffected() == 0 {
		return persistence.ErrRecordNotFound
	}

	return nil
}

func (r *MatchRepository) Update(ctx context.Context, match model.Match) (model.Match, error) {
	query := `
		UPDATE matches
		SET home_team_id = $1, away_team_id = $2, home_odds = $3, away_odds = $4, home_score = $5, away_score = $6, starts_at = $7, status = $8, version = version + 1
		WHERE id = $9 AND version = $10
		RETURNING version
	`
	args := []any{
		match.HomeTeamID,
		match.AwayTeamID,
		match.Odds.Home,
		match.Odds.Away,
		match.Score.Home,
		match.Score.Away,
		match.StartsAt,
		match.Status,
		match.ID,
		match.Version,
	}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&match.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Match{}, persistence.ErrEditConflict
		case persistence.IsFKViolation(err):
			// The referenced team was deleted between the handler's existence
			// check and this write (season and round are not updatable, so
			// they cannot trigger the FK here).
			return model.Match{}, persistence.ErrRecordNotFound
		case persistence.IsTriggerViolation(err):
			// matches_freeze_gate: the match has started and the update tried
			// to regress it to created/postponed (ADR-008).
			return model.Match{}, persistence.ErrRecordInUse
		default:
			return model.Match{}, err
		}
	}

	return match, nil
}

// PhaseForMatch returns the ride-phase inputs for the round the given
// match belongs to, in one statement: the round status (the ErrRoundNotOpen
// gate input) plus the phase window — min(starts_at), max(ended_at) over
// the round's matches.
//
// One statement, not three reads: the round-status gate and the window
// come from the same snapshot with no explicit transaction, and the old
// match-to-round hop (Matches.Get → Rounds.Get → PhaseWindow) is gone.
// Phase stays derived in Go from these facts (ADR-017); the store returns
// only facts.
//
// GROUP BY r.id suffices for selecting r.status: r.id is rounds' primary
// key, so PostgreSQL treats every other rounds column as functionally
// dependent (single-valued per group).
func (r *MatchRepository) PhaseForMatch(ctx context.Context, matchID int) (model.RoundStatus, *time.Time, *time.Time, error) {
	// Schema-qualified: this statement runs on both pools — the
	// schedule's own (schedule_app) and, through the SchedulePort, game's
	// ride commands' phase gate — so it names the schema it reads across
	// instead of leaning on a search_path that differs per role.
	query := `
		SELECT r.status, min(m.starts_at), max(m.ended_at)
		FROM schedule.matches m
		INNER JOIN schedule.rounds r ON r.id = m.round_id
		WHERE m.id = $1
		GROUP BY r.id
	`
	var status model.RoundStatus
	var firstStartsAt, lastEndedAt *time.Time
	err := r.pool.QueryRow(ctx, query, matchID).Scan(&status, &firstStartsAt, &lastEndedAt)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return "", nil, nil, persistence.ErrRecordNotFound
		default:
			return "", nil, nil, err
		}
	}

	return status, firstStartsAt, lastEndedAt, nil
}

func (r *MatchRepository) NextForTeam(ctx context.Context, teamID int) (model.Match, error) {
	// Schema-qualified like PhaseForMatch: dual-pool statement (schedule's
	// own and, via the SchedulePort, game's continuation pick).
	query := `
		SELECT m.id, m.created_at, m.starts_at, m.ended_at, m.season_id, m.round_id, m.home_team_id, m.away_team_id, m.status, m.home_odds, m.away_odds, m.home_score, m.away_score, m.version
		FROM schedule.matches m
		INNER JOIN schedule.rounds r ON r.id = m.round_id
		WHERE (m.home_team_id = $1 OR m.away_team_id = $1) 
			AND m.ended_at IS NULL 
			AND r.status <> 'closed'
		ORDER BY m.starts_at ASC, m.id ASC
		LIMIT 1
	`

	var match model.Match

	err := r.pool.QueryRow(ctx, query, teamID).Scan(
		&match.ID,
		&match.CreatedAt,
		&match.StartsAt,
		&match.EndedAt,
		&match.SeasonID,
		&match.RoundID,
		&match.HomeTeamID,
		&match.AwayTeamID,
		&match.Status,
		&match.Odds.Home,
		&match.Odds.Away,
		&match.Score.Home,
		&match.Score.Away,
		&match.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return model.Match{}, persistence.ErrRecordNotFound
		default:
			return model.Match{}, err
		}
	}

	return match, nil
}

// ResolveTx is ResolveMatch's match half: the guarded score write. The
// WHERE re-verifies the contended facts at the write instant under READ
// COMMITTED — status still in_progress, match started, not yet ended, the
// caller's version still current, and the match's own round still open
// (resolution is a match-phase operation; the EXISTS is correlated to this
// match's round_id, not a free-standing "some open round exists"). A
// blocked write re-reads and re-evaluates on lock release (EvalPlanQual),
// so 0 rows means the race was lost or the facts never held — classified
// by classifyResolveRefusal, not guessed.
func (r *MatchRepository) ResolveTx(ctx context.Context, tx pgx.Tx, matchID int, score model.Score, endedAt time.Time, version int) (model.Match, error) {
	query := `
		UPDATE matches
		SET status = 'closed', home_score = $1, away_score = $2, ended_at = $3, version = version + 1
		WHERE id = $4 AND status = 'in_progress' AND starts_at < now() AND ended_at IS NULL AND version = $5
		AND EXISTS (
			SELECT 1
			FROM rounds r
			WHERE r.id = matches.round_id AND r.status = 'open')
		RETURNING created_at, starts_at, season_id, round_id, status, home_team_id, away_team_id, home_odds, away_odds, version
	`
	args := []any{score.Home, score.Away, endedAt, matchID, version}

	match := model.Match{
		ID:      matchID,
		Score:   score,
		EndedAt: &endedAt,
	}

	err := tx.QueryRow(ctx, query, args...).Scan(
		&match.CreatedAt,
		&match.StartsAt,
		&match.SeasonID,
		&match.RoundID,
		&match.Status,
		&match.HomeTeamID,
		&match.AwayTeamID,
		&match.Odds.Home,
		&match.Odds.Away,
		&match.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// 0 rows is ambiguous — unknown match, stale version, wrong status,
			// round no longer open — so name the cause before responding. The
			// classification reads on the pool beside the tx (per-statement
			// snapshot): the refusal already happened, the read only names the
			// reason (the classifyInsertRefusal convention).
			return model.Match{}, r.classifyResolveRefusal(ctx, matchID)
		default:
			return model.Match{}, err
		}
	}

	return match, nil
}

// classifyResolveRefusal distinguishes the 0-row causes of ResolveTx. An
// unknown match is ErrRecordNotFound (404); every surviving cause — stale
// version, status past in_progress, round closed — is ErrEditConflict
// (409): another resolution won, or the match moved on. Re-resolution of
// an already-closed match therefore surfaces as a clean conflict, the
// Phase 04 contract for two concurrent resolutions.
func (r *MatchRepository) classifyResolveRefusal(ctx context.Context, matchID int) error {
	var id int

	err := r.pool.QueryRow(ctx, `SELECT id FROM matches WHERE id = $1`, matchID).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return persistence.ErrRecordNotFound
	case err != nil:
		return err
	default:
		return persistence.ErrEditConflict
	}
}

// ResultsForRound builds the MatchResult payload set for a closing round —
// the fat body of the RoundCompleted event (ADR-027). The round-close gate
// guarantees every match of the round is closed with scores and ended_at;
// the status filter keeps the scan shape honest rather than trusting the
// gate. The rows are mapped to model results; the winner derivation is
// model.NewMatchResult's (one authoritative copy), so the event carries the
// outcome, not just raw numbers.
func (r *MatchRepository) ResultsForRound(ctx context.Context, roundID int) ([]model.MatchResult, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, season_id, round_id, home_team_id, away_team_id,
		       home_odds, away_odds, home_score, away_score, ended_at
		FROM matches
		WHERE round_id = $1 AND status = 'closed'
		ORDER BY id
	`, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []model.MatchResult{}
	for rows.Next() {
		var (
			matchID, seasonID, roundID, homeTeamID, awayTeamID int
			odds                                               model.Odds
			homeScore, awayScore                               int
			endedAt                                            time.Time
		)
		if err := rows.Scan(
			&matchID,
			&seasonID,
			&roundID,
			&homeTeamID,
			&awayTeamID,
			&odds.Home,
			&odds.Away,
			&homeScore,
			&awayScore,
			&endedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, model.NewMatchResult(matchID, seasonID, roundID, homeTeamID, awayTeamID, odds, homeScore, awayScore, endedAt))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
