package game

import "time"

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
		State:     "open",
		Version:   1,
	},
}
