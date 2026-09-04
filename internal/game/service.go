package game

import (
	"fmt"
	"time"
)

type Service struct {
	seasons []Season
}

func NewService() *Service {
	return &Service{
		seasons: seasonsData,
	}
}

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

func (s *Service) GetSeasons(ids []int) ([]Season, error) {
	var seasons []Season
	for _, id := range ids {
		for _, season := range s.seasons {
			if season.ID == id {
				seasons = append(seasons, season)
				break
			}
		}
	}

	if len(seasons) < 1 {
		err := fmt.Errorf("no seasons found with ids: %v", ids)
		return nil, err
	}

	return seasons, nil
}

type Team struct {
	ID          int       `json:"id"`
	CreatedAt   time.Time `json:"-"`
	Name        string    `json:"name"`
	Logo        string    `json:"logo"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}
