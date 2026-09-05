package game

import (
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

// Player is a game entrant scoped to a season — who plays, not who logs in
// (identity) and not a real-world basketball athlete (future Athlete entity).
type Player struct {
	ID              int       `json:"id"`
	CreatedAt       time.Time `json:"-"`
	SeasonID        int       `json:"season_id"`
	FavouriteTeamID int       `json:"favourite_team_id"`
	Version         int       `json:"version"`
}

var playersData = []Player{
	{
		ID:              1,
		CreatedAt:       time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:        2,
		FavouriteTeamID: 1, // Olympiacos
		Version:         1,
	},
	{
		ID:              2,
		CreatedAt:       time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:        2,
		FavouriteTeamID: 2, // Real Madrid
		Version:         1,
	},
}

func ValidatePlayer(v *validator.Validator, player Player) {
	v.Check(player.SeasonID > 0, "season_id", "must be provided")
	v.Check(player.FavouriteTeamID > 0, "favourite_team_id", "must be provided")
}

type PlayerStore struct {
	players []Player
}

func (s *PlayerStore) Get(id int) (Player, error) {
	if id < 1 {
		return Player{}, ErrRecordNotFound
	}

	for _, player := range s.players {
		if player.ID == id {
			return player, nil
		}
	}

	return Player{}, ErrRecordNotFound
}

func (s *PlayerStore) Insert(player Player) (Player, error) {
	player.ID = len(s.players) + 1
	player.CreatedAt = time.Now().UTC()
	player.Version = 1

	s.players = append(s.players, player)
	return player, nil
}
