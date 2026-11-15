package postgres

// The pre-refactor players SQL, unchanged: players are the game context's own
// rows, so no statement here crosses into the calendar. The calendar references
// (season, favourite team) are composed at the use-case layer through the
// Schedule port since 000016 dropped the cross-context FKs.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// PlayerRepository is the Postgres implementation of port.PlayerRepository.
type PlayerRepository struct {
	pool *pgxpool.Pool
}

var _ port.PlayerRepository = (*PlayerRepository)(nil)

func NewPlayerRepository(pool *pgxpool.Pool) *PlayerRepository {
	return &PlayerRepository{pool: pool}
}

func (r *PlayerRepository) GetAll(ctx context.Context, name string, favoriteTeamID int, filters persistence.Filters) ([]domain.Player, persistence.Metadata, error) {
	// Each filter is appended as a separate predicate (AND-composed) so the
	// planner can still use the per-column indexes — do not switch to a
	// catch-all like `WHERE (name ILIKE $1 OR $1 = '')`, which defeats the
	// trgm GIN index once the statement is cached (see ADR-006).
	conds, args := []string{}, []any{}

	if name != "" {
		args = append(args, "%"+name+"%")
		conds = append(conds, fmt.Sprintf("name ILIKE $%d", len(args)))
	}
	// favorite_team_id is never 0 in the schema (FK), so 0 doubles as the
	// "no filter" sentinel from the query-string default.
	if favoriteTeamID != 0 {
		args = append(args, favoriteTeamID)
		conds = append(conds, fmt.Sprintf("favorite_team_id = $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT count(*) OVER(), id, created_at, name, user_id, season_id, favorite_team_id, version
		FROM players%s
		ORDER BY %s %s, id ASC
		LIMIT $%d OFFSET $%d
	`, where, filters.SortColumn(), filters.SortDirection(), len(args)+1, len(args)+2)

	args = append(args, filters.Limit(), filters.Offset())

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, persistence.Metadata{}, err
	}

	defer rows.Close()

	totalRecords := 0
	players := []domain.Player{}

	for rows.Next() {
		player := domain.Player{}

		err := rows.Scan(
			&totalRecords,
			&player.ID,
			&player.CreatedAt,
			&player.Name,
			&player.UserID,
			&player.SeasonID,
			&player.FavoriteTeamID,
			&player.Version,
		)
		if err != nil {
			return nil, persistence.Metadata{}, err
		}

		players = append(players, player)
	}

	if err = rows.Err(); err != nil {
		return nil, persistence.Metadata{}, err
	}

	metadata := persistence.CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return players, metadata, nil
}

func (r *PlayerRepository) Get(ctx context.Context, id int) (domain.Player, error) {
	if id < 1 {
		return domain.Player{}, domain.ErrNotFound
	}

	query := `
		SELECT id, created_at, name, user_id, season_id, favorite_team_id, version
		FROM players
		WHERE id = $1
	`
	var player domain.Player

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&player.ID,
		&player.CreatedAt,
		&player.Name,
		&player.UserID,
		&player.SeasonID,
		&player.FavoriteTeamID,
		&player.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.Player{}, domain.ErrNotFound
		default:
			return domain.Player{}, err
		}
	}

	return player, nil
}

func (r *PlayerRepository) Insert(ctx context.Context, player domain.Player) (domain.Player, error) {
	query := `
		INSERT INTO players (name, user_id, season_id, favorite_team_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, version
	`
	args := []any{player.Name, player.UserID, player.SeasonID, player.FavoriteTeamID}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&player.ID, &player.CreatedAt, &player.Version)
	if err != nil {
		switch {
		case persistence.IsUniqueViolation(err, "players_season_id_user_id_key"):
			// A second registration of the same user in the same season.
			return domain.Player{}, domain.ErrDuplicateRecord
		case persistence.IsFKViolation(err):
			// Since 000016 there is no cross-context FK left on players; this
			// branch stays as the generic race mapping shape for when a
			// same-context FK reappears.
			return domain.Player{}, domain.ErrNotFound
		default:
			return domain.Player{}, err
		}
	}

	return player, nil
}

func (r *PlayerRepository) Update(ctx context.Context, player domain.Player) (domain.Player, error) {
	query := `
		UPDATE players
		SET name = $1, season_id = $2, favorite_team_id = $3, version = version + 1
		WHERE id = $4 AND version = $5
		RETURNING user_id, version
	`

	args := []any{player.Name, player.SeasonID, player.FavoriteTeamID, player.ID, player.Version}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&player.UserID, &player.Version)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.Player{}, domain.ErrEditConflict
		case persistence.IsFKViolation(err):
			// Since 000016 no FK is left on players; the team existence check
			// is the use case's, before this write.
			return domain.Player{}, domain.ErrNotFound
		default:
			return domain.Player{}, err
		}
	}

	return player, nil
}

func (r *PlayerRepository) Delete(ctx context.Context, id, seasonID int) error {
	query := `
		DELETE FROM players
		WHERE id = $1 AND season_id = $2
	`
	result, err := r.pool.Exec(ctx, query, id, seasonID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// ExistsForTeam reports whether any player names the team as their favourite
// — the game half of the calendar's team-delete refusal (000016: the
// players_favorite_team_id_fkey RESTRICT is gone with the cross-context FKs,
// so the "team still referenced" check is a composition of the calendar's
// and game's facts).
func (r *PlayerRepository) ExistsForTeam(ctx context.Context, teamID int) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM players WHERE favorite_team_id = $1)`, teamID).Scan(&exists)
	return exists, err
}

// DeleteForSeason deletes every player registered in the season — the game
// half of the calendar's season-delete cascade (000016: the FK CASCADE is
// gone with the cross-context FKs, so the season's players go through game
// before the season row; the row outcome is the old cascade's). A ride still
// referencing a player trips rides_player_id_fkey — a same-context FK — so a
// season that owns rides is refused: domain.ErrRecordInUse, the 409 the old
// cascade produced.
func (r *PlayerRepository) DeleteForSeason(ctx context.Context, seasonID int) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM players WHERE season_id = $1`, seasonID)
	if err != nil {
		if persistence.IsDeleteRestricted(err) {
			return domain.ErrRecordInUse
		}
		return err
	}
	_ = result
	return nil
}
