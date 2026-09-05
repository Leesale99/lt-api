package game

import (
	"fmt"
)

type Service struct {
	seasons []Season
	teams   []Team
}

func NewService() *Service {
	return &Service{
		seasons: seasonsData,
		teams:   teamsData,
	}
}

func (s *Service) GetSeason(id int) (Season, error) {
	for _, s := range seasonsData {
		if s.ID == id {
			return s, nil
		}
	}

	err := fmt.Errorf("no season found with id: %v", id)
	return Season{}, err
}

func (s *Service) GetTeam(id int) (Team, error) {
	for _, t := range s.teams {
		if t.ID == id {
			return t, nil
		}
	}

	err := fmt.Errorf("no team found with id: %v", id)
	return Team{}, err
}
