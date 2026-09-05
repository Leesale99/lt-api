package game

import (
	"errors"
)

var ErrRecordNotFound = errors.New("record not found")

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

func (s *Service) GetTeam(id int) (Team, error) {
	if id < 1 {
		return Team{}, ErrRecordNotFound
	}

	for _, t := range s.teams {
		if t.ID == id {
			return t, nil
		}
	}

	return Team{}, ErrRecordNotFound
}
