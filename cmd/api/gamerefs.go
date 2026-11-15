package main

// gamerefs.go is the calendar's consumer-side port adapter: schedule's delete
// policies ask game whether a team/match/season is still referenced, and game
// answers in ITS sentinels. This file is the one place the two vocabularies
// meet — game.ErrRecordInUse becomes schedule.ErrRecordInUse — so neither
// module imports the other's errors and the delete HTTP responses stay
// byte-identical (still 409 / 404).

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/schedule"
)

// gameRefs satisfies schedule's service.GameUsage over the game module facade.
type gameRefs struct{ game *game.Module }

func (g gameRefs) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.PlayersExistForTeam(ctx, teamID)
}

func (g gameRefs) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.RidesExistForTeam(ctx, teamID)
}

func (g gameRefs) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return g.game.RidesExistForMatch(ctx, matchID)
}

// DeletePlayersForSeasonTx replays the game half of the season-delete
// cascade on schedule's transaction (game's definer function, migration
// 000021), so the season row and its registrations commit or roll back
// together. A ride still referencing one of the players is game's
// record-in-use refusal; the calendar's policy and handler only know their
// own sentinel, so the translation happens here.
func (g gameRefs) DeletePlayersForSeasonTx(ctx context.Context, tx pgx.Tx, seasonID int) error {
	err := g.game.DeletePlayersForSeasonTx(ctx, tx, seasonID)
	if errors.Is(err, game.ErrRecordInUse) {
		return schedule.ErrRecordInUse
	}
	return err
}
