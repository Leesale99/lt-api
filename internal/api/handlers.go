package api

import (
	"net/http"

	game "lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/validator"
)

func (app *Application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data := envelope{
		"status":      "available",
		"environment": app.Env,
		"version":     app.Version,
	}

	err := app.writeJSON(w, http.StatusOK, data, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createSeasonHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		State string `json:"state"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	season := game.Season{
		State: input.State,
	}

	v := validator.New()

	if game.ValidateSeason(v, season); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	season, err = app.Game.InsertSeason(season)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"season": season}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showSeasonHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	season, err := app.Game.GetSeason(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"season": season}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createTeamHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Logo        string `json:"logo"`
		Description string `json:"description"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	team := game.Team{
		Name:        input.Name,
		Logo:        input.Logo,
		Description: input.Description,
	}

	v := validator.New()

	if game.ValidateTeam(v, team); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	team, err = app.Game.InsertTeam(team)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showTeamHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	team, err := app.Game.GetTeam(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"team": team}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createRoundHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Number int    `json:"number"`
		Status string `json:"status"`
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	if _, err := app.Game.GetSeason(seasonID); err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	round := game.Round{
		SeasonID: seasonID,
		Number:   input.Number,
		Status:   input.Status,
	}

	v := validator.New()

	if game.ValidateRound(v, round); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	round, err = app.Game.InsertRound(round)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showRoundHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	id, err := app.readIDParam(r, "round_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	round, err := app.Game.GetRound(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	if round.SeasonID != seasonID {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"round": round}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createMatchHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RoundID    int         `json:"round_id"`
		HomeTeamID int         `json:"home_team_id"`
		AwayTeamID int         `json:"away_team_id"`
		Status     string      `json:"status"`
		Odds       game.Odds   `json:"odds"`
		Score      *game.Score `json:"score"`
	}

	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	if _, err := app.Game.GetSeason(seasonID); err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	match := game.Match{
		SeasonID:   seasonID,
		RoundID:    input.RoundID,
		HomeTeamID: input.HomeTeamID,
		AwayTeamID: input.AwayTeamID,
		Status:     input.Status,
		Odds:       input.Odds,
		Score:      input.Score,
	}

	v := validator.New()

	if game.ValidateMatch(v, match); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	match, err = app.Game.InsertMatch(match)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) showMatchHandler(w http.ResponseWriter, r *http.Request) {
	seasonID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	id, err := app.readIDParam(r, "match_id")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	match, err := app.Game.GetMatch(id)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	if match.SeasonID != seasonID {
		app.notFoundResponse(w, r)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"match": match}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
