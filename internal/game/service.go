package game

import (
	"errors"
	"time"
)

var ErrRecordNotFound = errors.New("record not found")

type Service struct {
	seasons []Season
	rounds  []Round
	matches []Match
	teams   []Team
	players []Player
}

func NewService() *Service {
	return &Service{
		seasons: seasonsData,
		rounds:  roundsData,
		matches: matchesData,
		teams:   teamsData,
		players: playersData,
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

func (s *Service) InsertSeason(season Season) (Season, error) {
	season.ID = len(s.seasons) + 1
	season.CreatedAt = time.Now().UTC()
	season.Version = 1

	s.seasons = append(s.seasons, season)
	return season, nil
}

func (s *Service) GetRound(id int) (Round, error) {
	if id < 1 {
		return Round{}, ErrRecordNotFound
	}

	for _, r := range s.rounds {
		if r.ID == id {
			return r, nil
		}
	}

	return Round{}, ErrRecordNotFound
}

func (s *Service) InsertRound(round Round) (Round, error) {
	round.ID = len(s.rounds) + 1
	round.CreatedAt = time.Now().UTC()
	round.Version = 1

	s.rounds = append(s.rounds, round)
	return round, nil
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

func (s *Service) InsertTeam(team Team) (Team, error) {
	team.ID = len(s.teams) + 1
	team.CreatedAt = time.Now().UTC()
	team.Version = 1

	s.teams = append(s.teams, team)
	return team, nil
}

func (s *Service) GetMatch(id int) (Match, error) {
	if id < 1 {
		return Match{}, ErrRecordNotFound
	}

	for _, m := range s.matches {
		if m.ID == id {
			return m, nil
		}
	}

	return Match{}, ErrRecordNotFound
}

func (s *Service) InsertMatch(match Match) (Match, error) {
	match.ID = len(s.matches) + 1
	match.CreatedAt = time.Now().UTC()
	match.Version = 1

	s.matches = append(s.matches, match)
	return match, nil
}

func (s *Service) GetPlayer(id int) (Player, error) {
	if id < 1 {
		return Player{}, ErrRecordNotFound
	}

	for _, p := range s.players {
		if p.ID == id {
			return p, nil
		}
	}

	return Player{}, ErrRecordNotFound
}

func (s *Service) InsertPlayer(player Player) (Player, error) {
	player.ID = len(s.players) + 1
	player.CreatedAt = time.Now().UTC()
	player.Version = 1

	s.players = append(s.players, player)
	return player, nil
}
