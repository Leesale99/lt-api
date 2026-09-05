package game

import (
	"strings"
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

type Season struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"-"`
	State     string    `json:"state"`
	Version   int       `json:"version"`
}

var seasonsData = []Season{
	{
		ID:        1,
		CreatedAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		State:     "closed",
		Version:   1,
	},
	{
		ID:        2,
		CreatedAt: time.Date(2026, time.August, 28, 15, 30, 0, 0, time.UTC),
		State:     "registration_open",
		Version:   1,
	},
}

var seasonStates = []string{"created", "registration_open", "in_progress", "closed"}

func ValidateSeason(v *validator.Validator, season Season) {
	state := strings.ToLower(season.State)

	v.Check(state != "", "state", "must be provided")
	v.Check(validator.PermittedValue(state, seasonStates...), "state", "Must be one of: created, registration_open, in_progress, closed")
}

type SeasonStore struct {
	seasons []Season
}

func (s *SeasonStore) Get(id int) (Season, error) {
	if id < 1 {
		return Season{}, ErrRecordNotFound
	}

	for _, season := range s.seasons {
		if season.ID == id {
			return season, nil
		}
	}

	return Season{}, ErrRecordNotFound
}

func (s *SeasonStore) Insert(season Season) (Season, error) {
	season.ID = len(s.seasons) + 1
	season.CreatedAt = time.Now().UTC()
	season.Version = 1

	s.seasons = append(s.seasons, season)
	return season, nil
}
