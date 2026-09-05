package game

import (
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

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
	v.Check(round.Number > 0 && round.Number <= 38, "number", "must be between 1 and 38")
	v.Check(round.Status != "", "status", "must be provided")
	v.Check(validator.PermittedValue(round.Status, "open", "closed"), "status", "must be one of: open, closed")
}
