package game

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/validator"
)

type Odds struct {
	Home float64 `json:"home"`
	Away float64 `json:"away"`
}

type Score struct {
	Home *int `json:"home"`
	Away *int `json:"away"`
}

type Match struct {
	ID         int       `json:"id"`
	CreatedAt  time.Time `json:"-"`
	StartsAt   time.Time `json:"starts_at"`
	SeasonID   int       `json:"season_id"`
	RoundID    int       `json:"round_id"`
	HomeTeamID int       `json:"home_team_id"`
	AwayTeamID int       `json:"away_team_id"`
	Status     string    `json:"status"`
	Odds       Odds      `json:"odds"`
	Score      Score     `json:"score"`
	Version    int       `json:"version"`
}

func (m Match) Winner() *int {
	switch {
	case m.Status != "closed":
		return nil
	case *m.Score.Home > *m.Score.Away:
		return &m.HomeTeamID
	case *m.Score.Away > *m.Score.Home:
		return &m.AwayTeamID
	default:
		return nil
	}
}

var matchStatuses = []string{"created", "open", "in_progress", "postponed", "closed"}

// validateMatchShape checks the time-independent invariants of a match:
// required fields, allowed statuses, the status/score matrix and the odds
// bounds. It is the shared core of ValidateNewMatch and ValidateMatchUpdate;
// each of those adds the rules that differ between creating and editing.
func validateMatchShape(v *validator.Validator, match Match) {
	v.Check(match.RoundID > 0, "round_id", "must be provided")
	v.Check(match.SeasonID > 0, "season_id", "must be provided")
	v.Check(match.HomeTeamID > 0, "home_team_id", "must be provided")
	v.Check(match.AwayTeamID > 0, "away_team_id", "must be provided")
	v.Check(match.HomeTeamID != match.AwayTeamID, "home_team_id", "home and away team cannot be the same")
	v.Check(match.Status != "", "status", "must be provided")
	v.Check(validator.PermittedValue(match.Status, matchStatuses...), "status", "must be one of: created, open, in_progress, postponed, closed")
	v.Check(!match.StartsAt.IsZero(), "starts_at", "must be provided")
	bothNil := match.Score.Home == nil && match.Score.Away == nil
	bothSet := match.Score.Home != nil && match.Score.Away != nil
	v.Check(bothNil || bothSet, "score", "must contain both home and away values or neither")
	v.Check(!bothSet || (*match.Score.Home >= 0 && *match.Score.Away >= 0), "score", "must not be negative")
	switch {
	case match.Status == "in_progress" || match.Status == "closed":
		v.Check(match.Score.Home != nil, "score", "must be provided when the match is in progress or closed")
	default: // created, open, postponed
		v.Check(match.Score.Home == nil, "score", "must not be set before the match is in progress or closed")
	}
	v.Check(match.Odds.Home > 1 && match.Odds.Away > 1, "odds", "must both be greater than one")
}

// ValidateNewMatch validates a match about to be created. The `now` argument
// is injected so tests control the clock instead of racing time.Now().
func ValidateNewMatch(v *validator.Validator, match Match, now time.Time) {
	validateMatchShape(v, match)
	if !match.StartsAt.IsZero() {
		v.Check(match.StartsAt.After(now), "starts_at", "must be in the future")
	}
}

// matchStatusRank orders the statuses along their lifecycle so that backward
// transitions can be rejected. open and postponed share a rank: a match may
// move between them freely in either direction. closed sits at the top and is
// terminal, which the >= rank rule already enforces.
var matchStatusRank = map[string]int{
	"created":     0,
	"open":        1,
	"postponed":   1,
	"in_progress": 2,
	"closed":      3,
}

// ValidateMatchUpdate validates a match about to be updated (new) against the
// currently stored version (old). Unlike creation, starts_at may be in the
// past — an existing match keeps being editable after it has started — but
// status must never move backwards along the lifecycle, and a closed match is
// terminal.
func ValidateMatchUpdate(v *validator.Validator, old, new Match) {
	validateMatchShape(v, new)

	// The old status comes from the database and is trusted; the new one has
	// already been shape-checked above.
	oldRank, oldOK := matchStatusRank[old.Status]
	newRank, newOK := matchStatusRank[new.Status]
	if !oldOK || !newOK {
		return
	}
	v.Check(old.Status != "closed" || new.Status == "closed", "status", "cannot be changed after the match is closed")
	v.Check(newRank >= oldRank, "status", "cannot move to an earlier stage of the match lifecycle")
}

type MatchStore struct {
	pool *pgxpool.Pool
}

func (s *MatchStore) Get(ctx context.Context, id int) (Match, error) {
	if id < 1 {
		return Match{}, ErrRecordNotFound
	}

	query := `
		SELECT  id, created_at, starts_at, season_id, round_id, home_team_id, away_team_id, status, home_odds, away_odds, home_score, away_score, version
		FROM matches
		WHERE id = $1
	`

	var match Match

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&match.ID,
		&match.CreatedAt,
		&match.StartsAt,
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
			return Match{}, ErrRecordNotFound
		default:
			return Match{}, err
		}
	}

	return match, nil
}

func (s *MatchStore) Insert(ctx context.Context, match Match) (Match, error) {
	query := `
		INSERT INTO matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, home_score, away_score, starts_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, version
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
		match.Status,
	}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&match.ID, &match.CreatedAt, &match.Version)

	return match, err
}

func (s *MatchStore) Update(ctx context.Context, match Match) (Match, error) {
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

	err := s.pool.QueryRow(ctx, query, args...).Scan(&match.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Match{}, ErrEditConflict
		case fkViolation(err):
			// The referenced team was deleted between the handler's existence
			// check and this write (season and round are not updatable, so
			// they cannot trigger the FK here).
			return Match{}, ErrRecordNotFound
		default:
			return Match{}, err
		}
	}

	return match, nil
}
