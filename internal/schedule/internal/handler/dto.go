// Package handler holds schedule's HTTP layer: one file per entity (route
// handlers), the request/response DTOs, the schedule-flavored error writers,
// and the module's route table. Handlers depend on the service layer and
// never on the repositories; validation of request shape runs here, and
// policy/lifecycle decisions live in the service.
package handler

import (
	"time"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

// The DTOs are the frozen wire format. The model structs carry no json tags:
// these structs replicate the tags the pre-refactor domain structs had —
// VERBATIM, oddities included — so the HTTP surface is byte-identical while
// the model stays transport-free. Do not "clean up" a tag: it is a contract.

type seasonDTO struct {
	ID        int                `json:"id"`
	CreatedAt time.Time          `json:"-"`
	Status    model.SeasonStatus `json:"status"`
	Version   int                `json:"version"`
}

func seasonFromModel(season model.Season) seasonDTO {
	return seasonDTO{
		ID:        season.ID,
		CreatedAt: season.CreatedAt,
		Status:    season.Status,
		Version:   season.Version,
	}
}

func seasonsFromModel(seasons []model.Season) []seasonDTO {
	out := make([]seasonDTO, 0, len(seasons))
	for _, season := range seasons {
		out = append(out, seasonFromModel(season))
	}
	return out
}

type roundDTO struct {
	ID        int               `json:"id"`
	CreatedAt time.Time         `json:"-"`
	SeasonID  int               `json:"season_id"`
	Number    int               `json:"number"`
	Status    model.RoundStatus `json:"status"`
	Version   int               `json:"version"`
}

func roundFromModel(round model.Round) roundDTO {
	return roundDTO{
		ID:        round.ID,
		CreatedAt: round.CreatedAt,
		SeasonID:  round.SeasonID,
		Number:    round.Number,
		Status:    round.Status,
		Version:   round.Version,
	}
}

func roundsFromModel(rounds []model.Round) []roundDTO {
	out := make([]roundDTO, 0, len(rounds))
	for _, round := range rounds {
		out = append(out, roundFromModel(round))
	}
	return out
}

// oddsDTO and scoreDTO are the frozen shapes of the two value objects the
// model keeps tag-free (the model's Odds/Score are plain structs; these carry
// the wire keys).
type oddsDTO struct {
	Home float64 `json:"home"`
	Away float64 `json:"away"`
}

type scoreDTO struct {
	Home *int `json:"home"`
	Away *int `json:"away"`
}

func oddsFromModel(odds model.Odds) oddsDTO {
	return oddsDTO{Home: odds.Home, Away: odds.Away}
}

func oddsToModel(odds oddsDTO) model.Odds {
	return model.Odds{Home: odds.Home, Away: odds.Away}
}

func scoreFromModel(score model.Score) scoreDTO {
	return scoreDTO{Home: score.Home, Away: score.Away}
}

func scoreToModel(score scoreDTO) model.Score {
	return model.Score{Home: score.Home, Away: score.Away}
}

type matchDTO struct {
	ID         int               `json:"id"`
	CreatedAt  time.Time         `json:"-"`
	StartsAt   time.Time         `json:"starts_at"`
	EndedAt    *time.Time        `json:"_"` // frozen wire format: the key is "_", not "ended_at"
	SeasonID   int               `json:"season_id"`
	RoundID    int               `json:"round_id"`
	HomeTeamID int               `json:"home_team_id"`
	AwayTeamID int               `json:"away_team_id"`
	Status     model.MatchStatus `json:"status"`
	Odds       oddsDTO           `json:"odds"`
	Score      scoreDTO          `json:"score"`
	Version    int               `json:"version"`
}

func matchFromModel(match model.Match) matchDTO {
	return matchDTO{
		ID:         match.ID,
		CreatedAt:  match.CreatedAt,
		StartsAt:   match.StartsAt,
		EndedAt:    match.EndedAt,
		SeasonID:   match.SeasonID,
		RoundID:    match.RoundID,
		HomeTeamID: match.HomeTeamID,
		AwayTeamID: match.AwayTeamID,
		Status:     match.Status,
		Odds:       oddsFromModel(match.Odds),
		Score:      scoreFromModel(match.Score),
		Version:    match.Version,
	}
}

func matchesFromModel(matches []model.Match) []matchDTO {
	out := make([]matchDTO, 0, len(matches))
	for _, match := range matches {
		out = append(out, matchFromModel(match))
	}
	return out
}

type teamDTO struct {
	ID          int       `json:"id"`
	CreatedAt   time.Time `json:"-"`
	Name        string    `json:"name"`
	Logo        string    `json:"logo"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}

func teamFromModel(team model.Team) teamDTO {
	return teamDTO{
		ID:          team.ID,
		CreatedAt:   team.CreatedAt,
		Name:        team.Name,
		Logo:        team.Logo,
		Description: team.Description,
		Version:     team.Version,
	}
}

func teamsFromModel(teams []model.Team) []teamDTO {
	out := make([]teamDTO, 0, len(teams))
	for _, team := range teams {
		out = append(out, teamFromModel(team))
	}
	return out
}

// Request payloads. Their tags are the handlers' own (the model carries
// none), and they are the pre-refactor handlers' input structs verbatim.

// createSeasonRequest is deliberately empty: new seasons are always created,
// the client cannot set the status (see model.ValidateNewSeason). Update owns
// the rest of the lifecycle.
type createSeasonRequest struct{}

type updateSeasonRequest struct {
	Status *model.SeasonStatus `json:"status"`
}

// createRoundRequest has no status field: new rounds are always created, the
// client cannot set it (see model.ValidateNewRound). Opening a round is a
// PATCH that must also start the season, so creating directly as open would
// bypass that transaction.
type createRoundRequest struct {
	Number int `json:"number"`
}

// updateRoundRequest: season_id is not part of the input — it is immutable
// after creation (see the round repository's Update for the composite-FK
// reason).
type updateRoundRequest struct {
	Number *int               `json:"number"`
	Status *model.RoundStatus `json:"status"`
}

// createMatchRequest has no status field: new matches are always created,
// the client cannot set it (see model.ValidateNewMatch). Update owns the rest
// of the lifecycle.
type createMatchRequest struct {
	RoundID    int       `json:"round_id"`
	HomeTeamID int       `json:"home_team_id"`
	AwayTeamID int       `json:"away_team_id"`
	StartsAt   time.Time `json:"starts_at"`
	Odds       oddsDTO   `json:"odds"`
	Score      scoreDTO  `json:"score"`
}

type updateMatchRequest struct {
	StartsAt   *time.Time         `json:"starts_at"`
	HomeTeamID *int               `json:"home_team_id"`
	AwayTeamID *int               `json:"away_team_id"`
	Status     *model.MatchStatus `json:"status"`
	Odds       *oddsDTO           `json:"odds"`
	Score      *scoreDTO          `json:"score"`
}

// resolveMatchRequest is the resolve body: ended_at arrives as "ended_at"
// here — unlike the response's frozen "_" key (see matchDTO); both are part
// of the wire contract.
type resolveMatchRequest struct {
	EndedAt *time.Time `json:"ended_at"`
	Score   scoreDTO   `json:"score"`
	Version int        `json:"version"`
}

type createTeamRequest struct {
	Name        string `json:"name"`
	Logo        string `json:"logo"`
	Description string `json:"description"`
}

type updateTeamRequest struct {
	Name        *string `json:"name"`
	Logo        *string `json:"logo"`
	Description *string `json:"description"`
}
