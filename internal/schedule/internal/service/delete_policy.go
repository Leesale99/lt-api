package service

import (
	"context"
	"errors"

	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// GameUsage is the consumer-side port the delete policies consult: the game
// context's answer to "is this schedule record still referenced, and what
// must go with it?". It is declared here — at the consumer — with exactly the
// capabilities the policies use, so schedule never imports game (game →
// schedule is the only legal direction, ADR-028).
//
// The composition root adapts the game store to it until game exposes a
// facade of its own; the adapter is behavior-identical to the api handlers'
// former inline calls, including which errors pass through.
type GameUsage interface {
	// PlayersExistForTeam reports whether any player favours the team.
	PlayersExistForTeam(ctx context.Context, teamID int) (bool, error)
	// RidesExistForTeam reports whether any ride rides on the team.
	RidesExistForTeam(ctx context.Context, teamID int) (bool, error)
	// RidesExistForMatch reports whether any ride rides on the match.
	RidesExistForMatch(ctx context.Context, matchID int) (bool, error)
	// DeletePlayersForSeason deletes every player of the season.
	DeletePlayersForSeason(ctx context.Context, seasonID int) error
}

// errGameRefsNotWired is the composition fault: the delete policies cannot
// answer the cross-context question without their port.
var errGameRefsNotWired = errors.New("schedule: game usage port is not wired")

// DeleteTeam removes a team unless the game context still references it.
// teams is referenced with ON DELETE RESTRICT by matches (schedule-internal,
// still enforced by the DB), while the players_favorite_team_id_fkey and
// rides_team_id_fkey RESTRICTs are gone with the cross-context FKs
// (migration 000016) — so the "team still referenced by the game context"
// refusal is composed here: schedule holds its own store, game answers its
// own references (ADR-018). Same ErrRecordInUse → 409 surface the FK
// produced. The race — a player or ride created between the check and the
// delete — is the accepted gap (the same window the delete-then-FK flow had
// between its own read and write).
func (s *Service) DeleteTeam(ctx context.Context, id int) error {
	if s.usage == nil {
		return errGameRefsNotWired
	}

	playerRefs, err := s.usage.PlayersExistForTeam(ctx, id)
	if err != nil {
		return err
	}
	rideRefs, err := s.usage.RidesExistForTeam(ctx, id)
	if err != nil {
		return err
	}
	if playerRefs || rideRefs {
		return persistence.ErrRecordInUse
	}

	return s.teams.Delete(ctx, id)
}

// DeleteMatch removes a match unless the game context still has rides on it.
// rides_match_id_fkey's RESTRICT is gone with the cross-context FKs
// (migration 000016), so the "match still has rides" refusal is composed
// here: schedule holds its own store, game answers its own references
// (ADR-018: a match with rides on it must not be deleted — the tokens would
// vanish with it). Same ErrRecordInUse → 409 surface the FK produced. The
// race — a ride locked between the check and the delete — is the accepted
// gap (the same window the delete-then-FK flow had between its own read and
// write).
func (s *Service) DeleteMatch(ctx context.Context, matchID, seasonID int) error {
	if s.usage == nil {
		return errGameRefsNotWired
	}

	rideRefs, err := s.usage.RidesExistForMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if rideRefs {
		return persistence.ErrRecordInUse
	}

	return s.matches.Delete(ctx, matchID, seasonID)
}

// DeleteSeason removes a season, replaying the game half of the old cascade
// first. players_season_id_fkey's CASCADE is gone with the cross-context FKs
// (migration 000016), so the season's players are deleted through game BEFORE
// the season. The row outcome is the old cascade's, and the old refusals keep
// their surfaces: a ride still referencing a player trips
// rides_player_id_fkey (a same-context FK) → ErrRecordInUse → the 409 the
// handler writes, exactly what the blocked cascade produced. The accepted
// gap: if the season delete itself then fails, the players are already gone
// (the old flow deleted everything or nothing in one statement). Only
// deleteable seasons (no started matches — the seasons_delete_gate) reach the
// players step.
func (s *Service) DeleteSeason(ctx context.Context, id int) error {
	if s.usage == nil {
		return errGameRefsNotWired
	}

	if err := s.usage.DeletePlayersForSeason(ctx, id); err != nil {
		return err
	}

	return s.seasons.Delete(ctx, id)
}
