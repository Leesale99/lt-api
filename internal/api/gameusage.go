package api

// The composition adapter for the schedule module's game-reference port.
//
// The schedule module's delete policies must ask the game context whether a
// team or match is still referenced before removing it (the cross-context
// FKs are gone, migration 000016). The port is declared on the consumer side
// (schedule's service.GameUsage) with exactly the capabilities the policies
// use; this adapter is the gateway's answer, reading the game store's own
// methods. When game exposes a facade of its own the adapter collapses into
// it, and schedule's call sites do not move.

import (
	"context"

	"lt-api.aleksrdvn.com/internal/game"
)

// GameUsageAdapter adapts the game store to schedule's delete-policy port.
type GameUsageAdapter struct {
	Store *game.Store
}

// PlayersExistForTeam reports whether any player favours the team
// (players_favorite_team_id_fkey's old RESTRICT half).
func (a GameUsageAdapter) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return a.Store.Players.ExistsForTeam(ctx, teamID)
}

// RidesExistForTeam reports whether any ride rides on the team
// (rides_team_id_fkey's old RESTRICT half).
func (a GameUsageAdapter) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return a.Store.Rides.ExistsForTeam(ctx, teamID)
}

// RidesExistForMatch reports whether any ride rides on the match
// (rides_match_id_fkey's old RESTRICT half).
func (a GameUsageAdapter) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return a.Store.Rides.ExistsForMatch(ctx, matchID)
}

// DeletePlayersForSeason deletes every player of the season
// (players_season_id_fkey's old CASCADE half).
func (a GameUsageAdapter) DeletePlayersForSeason(ctx context.Context, seasonID int) error {
	return a.Store.Players.DeleteForSeason(ctx, seasonID)
}
