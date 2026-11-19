package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
)

// GameUsage is the consumer-side port the delete policies consult: the game
// context's answer to "is this schedule record still referenced, and what
// must go with it?". It is declared here — at the consumer — with exactly the
// capabilities the policies use, so schedule never imports game (game →
// schedule is the only legal direction, ADR-028).
//
// The composition root adapts the game facade to it (the gameRefs adapter in
// cmd/api) because the two contexts speak different sentinel vocabularies:
// game's "still referenced" refusal arrives at the boundary as the calendar's
// own model.ErrRecordInUse, so the policies below — and the handlers above
// them — only ever see schedule's errors.
type GameUsage interface {
	// PlayersExistForTeam reports whether any player favours the team.
	PlayersExistForTeam(ctx context.Context, teamID int) (bool, error)
	// RidesExistForTeam reports whether any ride rides on the team.
	RidesExistForTeam(ctx context.Context, teamID int) (bool, error)
	// RidesExistForMatch reports whether any ride rides on the match.
	RidesExistForMatch(ctx context.Context, matchID int) (bool, error)
	// DeletePlayersForSeasonTx deletes every player of the season ON THE
	// CALLER'S TRANSACTION — the game context's half of the season delete.
	// It takes schedule's tx so that the registrations and the season row
	// commit or roll back together: the season delete is all-or-nothing
	// again (the pre-000016 cascade was one statement).
	DeletePlayersForSeasonTx(ctx context.Context, tx pgx.Tx, seasonID int) error
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
// rule:R-SCHEDULE-026
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
		return model.ErrRecordInUse
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
// rule:R-SCHEDULE-026
func (s *Service) DeleteMatch(ctx context.Context, matchID, seasonID int) error {
	if s.usage == nil {
		return errGameRefsNotWired
	}

	rideRefs, err := s.usage.RidesExistForMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if rideRefs {
		return model.ErrRecordInUse
	}

	return s.matches.Delete(ctx, matchID, seasonID)
}

// DeleteSeason removes a season and its registrations in ONE transaction,
// replaying the game half of the old cascade first. players_season_id_fkey's
// CASCADE is gone with the cross-context FKs (migration 000016), so the
// season's players are deleted through game BEFORE the season row — on this
// service's transaction (game's side is a SECURITY DEFINER function,
// migration 000021, executing as the game schema's owner inside this tx).
//
// All-or-nothing, like the cascade it replaces: two authoritative database
// refusals can end this tx, and both roll it back whole.
//   - rides_player_id_fkey (game-internal, RESTRICT, 23503): a ride still
//     references one of the players. Asked first, so a season that owns rides
//     is refused before its own row is touched.
//   - seasons_delete_gate (BEFORE DELETE, P0001, R-SCHEDULE-023): the season
//     is in_progress/closed or has a started match. It fires on the season
//     DELETE itself, i.e. after the players step — inside this same tx, so
//     the refusal takes the player delete with it instead of leaving the
//     season alive with its registrations gone.
//
// Both arrive as ErrRecordInUse → the same 409 the blocked cascade produced.
// rule:R-SCHEDULE-026
// rule:R-SCHEDULE-027
func (s *Service) DeleteSeason(ctx context.Context, id int) error {
	if s.usage == nil {
		return errGameRefsNotWired
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := s.usage.DeletePlayersForSeasonTx(ctx, tx, id); err != nil {
		return err
	}

	if err := s.seasons.DeleteTx(ctx, tx, id); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
