package service

// The ride read use cases and the reference capabilities the calendar
// consults. Reads own no transaction (per-statement snapshots are enough for
// a GET) and speak domain values, so the transport never reaches a
// repository.

import (
	"context"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// RideGet reads one ride — the show use case. domain.ErrNotFound when it does
// not exist.
func (s *Service) RideGet(ctx context.Context, id int) (domain.Ride, error) {
	return s.rides.Get(ctx, id)
}

// RideList reads the ride list under the given filters (zero values mean "no
// filter").
func (s *Service) RideList(ctx context.Context, id, playerID, teamID, matchID int, state domain.RideState, filters persistence.Filters) ([]domain.Ride, persistence.Metadata, error) {
	return s.rides.GetAll(ctx, id, playerID, teamID, matchID, state, filters)
}

// The calendar-reference capabilities. The calendar's delete policies ask the
// game context whether a team, match or season is still referenced before
// removing it (the cross-context FKs are gone, migration 000016). These are
// game's answers to that question — the facts it owns — exposed on the module
// facade exactly as the calendar's port declares them.

// PlayersExistForTeam reports whether any player favours the team
// (players_favorite_team_id_fkey's old RESTRICT half).
func (s *Service) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return s.players.ExistsForTeam(ctx, teamID)
}

// RidesExistForTeam reports whether any ride rides on the team
// (rides_team_id_fkey's old RESTRICT half).
func (s *Service) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return s.rides.ExistsForTeam(ctx, teamID)
}

// RidesExistForMatch reports whether any ride rides on the match
// (rides_match_id_fkey's old RESTRICT half).
func (s *Service) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return s.rides.ExistsForMatch(ctx, matchID)
}

// DeletePlayersForSeason deletes every player of the season
// (players_season_id_fkey's old CASCADE half). domain.ErrRecordInUse when a
// ride still references one of them (the same-context rides_player_id_fkey).
func (s *Service) DeletePlayersForSeason(ctx context.Context, seasonID int) error {
	return s.players.DeleteForSeason(ctx, seasonID)
}
