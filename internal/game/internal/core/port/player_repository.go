package port

// PlayerRepository is the consumer-side interface the player use cases
// drive; the Postgres adapter at adapter/outbound/postgres implements it.
// Values in, domain sentinels out — a pgx row or a SQLSTATE never crosses
// this line.

import (
	"context"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// PlayerRepository stores the game's players (season entrants).
type PlayerRepository interface {
	// GetAll reads the player list; name is a case-insensitive fragment,
	// favoriteTeamID 0 means "no filter".
	GetAll(ctx context.Context, name string, favoriteTeamID int, filters persistence.Filters) ([]domain.Player, persistence.Metadata, error)

	// Get reads one player. domain.ErrNotFound when it does not exist.
	Get(ctx context.Context, id int) (domain.Player, error)

	// Insert writes a registration. domain.ErrDuplicateRecord when the same
	// user is registered twice in the season.
	Insert(ctx context.Context, player domain.Player) (domain.Player, error)

	// Update writes the player under the optimistic version guard.
	Update(ctx context.Context, player domain.Player) (domain.Player, error)

	// Delete removes one registration of one season.
	Delete(ctx context.Context, id, seasonID int) error

	// ExistsForTeam reports whether any player favours the team — the game
	// half of the calendar's team-delete refusal.
	ExistsForTeam(ctx context.Context, teamID int) (bool, error)

	// DeleteForSeason deletes every player registered in the season — the
	// game half of the calendar's season-delete cascade. A ride still
	// referencing a player trips rides_player_id_fkey (a same-context FK):
	// domain.ErrRecordInUse, the refusal the old cascade produced.
	DeleteForSeason(ctx context.Context, seasonID int) error
}
