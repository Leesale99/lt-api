package domain

import (
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

// Player is a game entrant scoped to a season — who plays, not who logs in
// (identity) and not a real-world basketball athlete (future Athlete entity).
type Player struct {
	ID        int
	CreatedAt time.Time
	Name      string
	// Nullable: since 000016 there is no players_user_id FK (identity is
	// another context), so a deleted user no longer nulls the column in the
	// DB; the column stays nullable for the historical "no owner" shape.
	UserID         *int
	SeasonID       int
	FavoriteTeamID int
	Version        int
}

func ValidatePlayer(v *validator.Validator, player Player) {
	v.Check(player.SeasonID > 0, "season_id", "must be provided")
	v.Check(player.Name != "", "name", "must be provided")
	v.Check(len(player.Name) <= 200, "name", "must not be more than 200 bytes long")
	v.Check(player.FavoriteTeamID > 0, "favorite_team_id", "must be provided")
}

// PlayerPatch is the partial-update input: nil fields are unchanged. The
// patch rules (which fields may move, and that the merged row must still
// validate) are the update use case's, so the merged result — not the patch
// alone — is what ValidatePlayer sees.
type PlayerPatch struct {
	Name           *string
	FavoriteTeamID *int
}

// Apply overlays the patch's non-nil fields on player and returns the merged
// value.
func (p PlayerPatch) Apply(player Player) Player {
	if p.Name != nil {
		player.Name = *p.Name
	}
	if p.FavoriteTeamID != nil {
		player.FavoriteTeamID = *p.FavoriteTeamID
	}

	return player
}
