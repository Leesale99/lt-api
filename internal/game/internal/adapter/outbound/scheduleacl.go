// Package outbound holds the game module's outbound adapters that are not
// storage: the anti-corruption layers between the core's ports and the other
// bounded contexts it depends on. This is the ONLY game package allowed to
// import the schedule context — the calendar's types, payloads and sentinels
// stop here, translated into game's own vocabulary.
package outbound

// scheduleacl.go is the calendar anti-corruption layer: it wraps the
// schedule facade's game-facing surface (the module's PhaseForMatch /
// NextForTeam / RoundOpenTx / DestinationOpenTx / Match / Team / Season
// reads) and implements port.Schedule over game-owned types and sentinels.
//
// The error contract flip lives here: the calendar speaks its own sentinels
// (schedule.ErrRecordNotFound and friends, re-exported by its facade), game's
// core speaks its own, and this file is where one becomes the other. Every
// refusal the calendar can raise is named once, here, so no game call site
// checks a foreign error.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// ScheduleAPI is the schedule facade surface this ACL consumes — the
// structural subset of *schedule.Module the game module needs. Declaring it
// here (and only here) keeps the facade constructor free of a schedule
// import: the composition root hands the module over and Go's structural
// typing does the rest.
type ScheduleAPI interface {
	PhaseForMatch(ctx context.Context, matchID int) (schedule.RoundStatus, *time.Time, *time.Time, error)
	NextForTeam(ctx context.Context, teamID int) (schedule.Match, error)
	RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) (schedule.RoundStatus, error)
	DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error
	Match(ctx context.Context, matchID int) (schedule.Match, error)
	Team(ctx context.Context, teamID int) (schedule.Team, error)
	Season(ctx context.Context, seasonID int) (schedule.Season, error)
}

// ScheduleACL implements port.Schedule over the calendar facade.
type ScheduleACL struct {
	api ScheduleAPI
}

var _ port.Schedule = (*ScheduleACL)(nil)

func NewScheduleACL(api ScheduleAPI) *ScheduleACL {
	return &ScheduleACL{api: api}
}

// PhaseForMatch reports the ride phase of the match's round at the given
// instant. A round that is not open is the round-refusal, not a phase: the
// calendar has no phase for a closed round.
func (a *ScheduleACL) PhaseForMatch(ctx context.Context, matchID int, now time.Time) (domain.Phase, error) {
	status, firstStartsAt, lastEndedAt, err := a.api.PhaseForMatch(ctx, matchID)
	if err != nil {
		return "", translate(err)
	}
	if status != schedule.RoundOpen {
		return "", domain.ErrRoundNotOpen
	}

	phase, err := translatePhase(schedule.Phase(now, firstStartsAt, lastEndedAt))
	if err != nil {
		return "", err
	}

	return phase, nil
}

// NextForTeam returns the team's next live match; no destination is the
// domain's ErrNoNextMatch (a calendar conflict, not a missing ride).
func (a *ScheduleACL) NextForTeam(ctx context.Context, teamID int) (port.NextMatch, error) {
	match, err := a.api.NextForTeam(ctx, teamID)
	if err != nil {
		if errors.Is(err, schedule.ErrRecordNotFound) {
			return port.NextMatch{}, domain.ErrNoNextMatch
		}
		return port.NextMatch{}, err
	}

	return port.NextMatch{
		ID:         match.ID,
		StartsAt:   match.StartsAt,
		HomeTeamID: match.HomeTeamID,
		AwayTeamID: match.AwayTeamID,
	}, nil
}

// RoundOpenTx pins the ride's own round open inside the caller's
// transaction. Unknown match → ErrNotFound; a round that refuses commands →
// ErrRoundNotOpen (the round-refusal 409).
func (a *ScheduleACL) RoundOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	status, err := a.api.RoundOpenTx(ctx, tx, matchID)
	if err != nil {
		return translate(err)
	}
	if status != schedule.RoundOpen {
		return domain.ErrRoundNotOpen
	}

	return nil
}

// DestinationOpenTx pins a continuation destination live inside the caller's
// transaction. Every refusal cause (unknown, ended, round closed) is the
// calendar's not-found, which the use case maps to the round refusal.
func (a *ScheduleACL) DestinationOpenTx(ctx context.Context, tx pgx.Tx, matchID int) error {
	return translate(a.api.DestinationOpenTx(ctx, tx, matchID))
}

// Match returns the committed facts of one match.
func (a *ScheduleACL) Match(ctx context.Context, matchID int) (port.MatchSnapshot, error) {
	match, err := a.api.Match(ctx, matchID)
	if err != nil {
		return port.MatchSnapshot{}, translate(err)
	}

	return port.MatchSnapshot{
		ID:         match.ID,
		HomeTeamID: match.HomeTeamID,
		AwayTeamID: match.AwayTeamID,
		Score: port.Score{
			Home: match.Score.Home,
			Away: match.Score.Away,
		},
	}, nil
}

// Team reports one team's identity.
func (a *ScheduleACL) Team(ctx context.Context, teamID int) (port.Team, error) {
	team, err := a.api.Team(ctx, teamID)
	if err != nil {
		return port.Team{}, translate(err)
	}

	return port.Team{ID: team.ID, Name: team.Name}, nil
}

// Season reports one season's identity.
func (a *ScheduleACL) Season(ctx context.Context, seasonID int) (port.Season, error) {
	season, err := a.api.Season(ctx, seasonID)
	if err != nil {
		return port.Season{}, translate(err)
	}

	return port.Season{ID: season.ID}, nil
}

// translate maps the calendar's error vocabulary onto game's: an absent row
// is game's ErrNotFound, everything else passes through untouched (a
// connection failure is not a domain fact).
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, schedule.ErrRecordNotFound):
		return domain.ErrNotFound
	default:
		return err
	}
}

// translatePhase maps the calendar's phase names onto game's. The two
// vocabularies carry the same three semantics; an unknown name is a
// calendar defect, not a phase game can act on.
func translatePhase(phase schedule.RoundPhase) (domain.Phase, error) {
	switch phase {
	case schedule.ActionPhase:
		return domain.ActionPhase, nil
	case schedule.MatchPhase:
		return domain.MatchPhase, nil
	case schedule.DecisionPhase:
		return domain.DecisionPhase, nil
	default:
		return "", fmt.Errorf("calendar reported an unknown round phase %q", phase)
	}
}
