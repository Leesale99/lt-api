package service

// The player use cases: registration, listing, patch and delete. Player CRUD
// carries little rule, but two of its rules are business policy and live
// here rather than in the transport:
//
//   - authorization: the owner of a registration may always manage it, any
//     other principal only with players:write:any (admin). A player with no
//     owner (NULL user_id) is admin-only by design.
//   - reference composition: a season and a favourite team must exist in the
//     calendar (the cross-context FKs are gone, migration 000016), and a
//     refused reference keeps the 422 field error the FK refusal used to
//     produce.
//
// The transport hands the authenticated principal in as an Actor; nothing here
// knows how the request was authenticated.

import (
	"context"
	"errors"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// Actor is the authenticated principal a use case runs on behalf of: the user
// id writes are attributed to, and whether the principal's role carries
// players:write:any.
type Actor struct {
	Authenticated bool
	UserID        int
	CanManageAny  bool
}

// canManagePlayer reports whether the actor may modify the given player: the
// owner may always manage their own registration; any other actor only with
// players:write:any (admin). A player with a NULL user_id has no owner —
// admin-only by design.
func canManagePlayer(actor Actor, player domain.Player) bool {
	if actor.Authenticated && player.UserID != nil && *player.UserID == actor.UserID {
		return true
	}
	return actor.CanManageAny
}

// referenceTeam checks the favourite-team reference against the calendar. An
// unknown team is the field-level refusal the old FK produced: a 422 naming
// favorite_team_id, not a 404 (the player, not the team, is the resource).
func (s *Service) referenceTeam(ctx context.Context, teamID int) error {
	if _, err := s.schedule.Team(ctx, teamID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewValidationError(map[string]string{"favorite_team_id": "must reference an existing team"})
		}
		return err
	}
	return nil
}

// PlayerCreate registers the actor in the season: the season (URL) and the
// favourite team must exist in the calendar, and user_id is only ever the
// authenticated principal's. The registration's user existence is the
// transport's check (identity is not game's context to read).
func (s *Service) PlayerCreate(ctx context.Context, actor Actor, seasonID int, player domain.Player) (domain.Player, error) {
	if !actor.Authenticated {
		return domain.Player{}, domain.ErrForbidden
	}

	// The season comes from the URL path, never the body.
	if _, err := s.schedule.Season(ctx, seasonID); err != nil {
		return domain.Player{}, err
	}

	player.SeasonID = seasonID
	player.UserID = &actor.UserID

	if err := s.referenceTeam(ctx, player.FavoriteTeamID); err != nil {
		return domain.Player{}, err
	}

	return s.players.Insert(ctx, player)
}

// PlayerGet reads one registration. domain.ErrNotFound when it does not exist.
func (s *Service) PlayerGet(ctx context.Context, id int) (domain.Player, error) {
	return s.players.Get(ctx, id)
}

// PlayerList reads the registration list (name is a case-insensitive
// fragment, favoriteTeamID 0 means "no filter").
func (s *Service) PlayerList(ctx context.Context, name string, favoriteTeamID int, filters persistence.Filters) ([]domain.Player, persistence.Metadata, error) {
	return s.players.GetAll(ctx, name, favoriteTeamID, filters)
}

// PlayerUpdate applies a patch under the actor's authority: the registration
// must exist in the season, the actor must be allowed to manage it, the
// client's expected version (nil = not claimed) must match, the merged row must
// still validate and its favourite team must exist. The merged row — not the
// patch — is what the version guard and the write see.
func (s *Service) PlayerUpdate(ctx context.Context, actor Actor, seasonID, playerID int, patch domain.PlayerPatch, expectedVersion *int) (domain.Player, error) {
	player, err := s.players.Get(ctx, playerID)
	if err != nil {
		return domain.Player{}, err
	}

	if player.SeasonID != seasonID {
		return domain.Player{}, domain.ErrNotFound
	}

	if !canManagePlayer(actor, player) {
		return domain.Player{}, domain.ErrForbidden
	}

	if expectedVersion != nil && *expectedVersion != player.Version {
		return domain.Player{}, domain.ErrEditConflict
	}

	merged := patch.Apply(player)

	v := validator.New()
	domain.ValidatePlayer(v, merged)
	if !v.Valid() {
		return domain.Player{}, domain.NewValidationError(v.Errors)
	}

	if err := s.referenceTeam(ctx, merged.FavoriteTeamID); err != nil {
		return domain.Player{}, err
	}

	return s.players.Update(ctx, merged)
}

// PlayerDelete removes a registration under the actor's authority: same
// existence, season and ownership rules as PlayerUpdate, then the row goes
// (a ride still referencing it trips the game-internal FK — the refusal stays
// the DB's, surfaced as a conflict by the transport).
func (s *Service) PlayerDelete(ctx context.Context, actor Actor, seasonID, playerID int) error {
	player, err := s.players.Get(ctx, playerID)
	if err != nil {
		return err
	}

	if player.SeasonID != seasonID {
		return domain.ErrNotFound
	}

	if !canManagePlayer(actor, player) {
		return domain.ErrForbidden
	}

	return s.players.Delete(ctx, playerID, seasonID)
}
