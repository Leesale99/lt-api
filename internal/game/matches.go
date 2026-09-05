package game

import (
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

type Odds struct {
	Home int `json:"home"`
	Away int `json:"away"`
}

type Score struct {
	Home int `json:"home"`
	Away int `json:"away"`
}

type Match struct {
	ID         int       `json:"id"`
	CreatedAt  time.Time `json:"-"`
	SeasonID   int       `json:"season_id"`
	RoundID    int       `json:"round_id"`
	HomeTeamID int       `json:"home_team_id"`
	AwayTeamID int       `json:"away_team_id"`
	Status     string    `json:"status"`
	Odds       Odds      `json:"odds"`
	Score      *Score    `json:"score"` // nil = match not played yet
	Version    int       `json:"version"`
}

func (m Match) Winner() *int {
	if m.Score == nil {
		return nil
	}
	if m.Score.Home > m.Score.Away {
		return &m.HomeTeamID
	}
	return &m.AwayTeamID
}

var matchesData = []Match{
	{
		ID:         1,
		CreatedAt:  time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:   1,
		RoundID:    1,
		HomeTeamID: 1, // Olympiacos
		AwayTeamID: 2, // Real Madrid
		Status:     "closed",
		Odds:       Odds{Home: 2, Away: 4},
		Score:      &Score{Home: 88, Away: 79}, // Olympiacos wins
		Version:    1,
	},
	{
		ID:         2,
		CreatedAt:  time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:   1,
		RoundID:    2,
		HomeTeamID: 2, // Real Madrid
		AwayTeamID: 1, // Olympiacos
		Status:     "open",
		Odds:       Odds{Home: 5, Away: 2},
		Score:      nil, // not played yet
		Version:    1,
	},
}

func ValidateMatch(v *validator.Validator, match Match) {
	v.Check(match.RoundID > 0, "round_id", "must be provided")
	v.Check(match.SeasonID > 0, "season_id", "must be provided")
	v.Check(match.HomeTeamID > 0, "home_team_id", "must be provided")
	v.Check(match.AwayTeamID > 0, "away_team_id", "must be provided")
	v.Check(validator.PermittedValue(match.Status, "open", "closed"), "status", "must be one of: open, closed")
	v.Check(match.Odds.Home > 0 && match.Odds.Away > 0, "odds", "must both be greater than zero")
	if match.Score != nil {
		v.Check(match.Score.Home >= 0 && match.Score.Away >= 0, "score", "must not be negative")
	}
}
