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

func ValidateMatch(v *validator.Validator, match Match, now time.Time) {
	v.Check(match.RoundID > 0, "round_id", "must be provided")
	v.Check(match.SeasonID > 0, "season_id", "must be provided")
	v.Check(match.HomeTeamID > 0, "home_team_id", "must be provided")
	v.Check(match.AwayTeamID > 0, "away_team_id", "must be provided")
	v.Check(match.HomeTeamID != match.AwayTeamID, "home_team_id", "home and away team cannot be the same")
	v.Check(match.Status != "", "status", "must be provided")
	v.Check(validator.PermittedValue(match.Status, matchStatuses...), "status", "must be one of: created, open, in_progress, postponed, closed")
	v.Check(!match.StartsAt.IsZero(), "starts_at", "must be provided")
	if !match.StartsAt.IsZero() {
		v.Check(match.StartsAt.After(now), "starts_at", "must be in the future")
	}
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
