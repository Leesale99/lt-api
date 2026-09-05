package game

import (
	"fmt"
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

// roundsPerSeason is the total number of rounds in a season. The league has
// 20 teams playing each other home and away (double round-robin): 38 rounds
// of 10 matches each.
const roundsPerSeason = 38

type Round struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"-"`
	SeasonID  int       `json:"season_id"`
	Number    int       `json:"number"`
	Status    string    `json:"status"`
	Version   int       `json:"version"`
}

var roundsData = []Round{
	{
		ID:        1,
		CreatedAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:  1,
		Number:    1,
		Status:    "closed",
		Version:   1,
	},
	{
		ID:        2,
		CreatedAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		SeasonID:  1,
		Number:    2,
		Status:    "open",
		Version:   1,
	},
}

func ValidateRound(v *validator.Validator, round Round) {
	v.Check(round.SeasonID > 0, "season_id", "must be provided")
	v.Check(round.Number > 0 && round.Number <= roundsPerSeason, "number", fmt.Sprintf("must be between 1 and %d", roundsPerSeason))
	v.Check(round.Status != "", "status", "must be provided")
	v.Check(validator.PermittedValue(round.Status, "open", "closed"), "status", "must be one of: open, closed")
}

type RoundStore struct {
	rounds []Round
}

func (s *RoundStore) Get(id int) (Round, error) {
	if id < 1 {
		return Round{}, ErrRecordNotFound
	}

	for _, round := range s.rounds {
		if round.ID == id {
			return round, nil
		}
	}

	return Round{}, ErrRecordNotFound
}

func (s *RoundStore) Insert(round Round) (Round, error) {
	round.ID = len(s.rounds) + 1
	round.CreatedAt = time.Now().UTC()
	round.Version = 1

	s.rounds = append(s.rounds, round)
	return round, nil
}
