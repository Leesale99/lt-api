package game

import (
	"net/url"
	"path"
	"strings"
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

type Team struct {
	ID          int       `json:"id"`
	CreatedAt   time.Time `json:"-"`
	Name        string    `json:"name"`
	Logo        string    `json:"logo"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}

var teamsData = []Team{
	{
		ID:          1,
		CreatedAt:   time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		Name:        "Olympiacos",
		Logo:        "http://example.com/small-logo.png",
		Description: "Some text here",
		Version:     1,
	},
	{
		ID:          2,
		CreatedAt:   time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
		Name:        "Real Madrid",
		Logo:        "http://example.com/small-logo.png",
		Description: "Some text here",
		Version:     1,
	},
}

func ValidateTeam(v *validator.Validator, team Team) {
	v.Check(team.Name != "", "name", "must be provided")
	v.Check(len(team.Name) <= 500, "name", "must not be more then 500 bytes long")
	v.Check(team.Logo != "", "logo", "must be provided")
	if team.Logo != "" {
		v.Check(validImageURL(team.Logo), "logo", "must be a valid image URL ending in png, jpg, jpeg, svg or webp")
	}
	v.Check(team.Description != "", "description", "must be provided")
	v.Check(len(team.Description) <= 5000, "description", "must not be more then 5000 bytes long")
}

func validImageURL(s string) bool {
	u, err := url.ParseRequestURI(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}

	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.EscapedPath()), "."))
	return validator.PermittedValue(ext, "png", "jpg", "jpeg", "svg", "webp")
}

type TeamStore struct {
	teams []Team
}

func (s *TeamStore) Get(id int) (Team, error) {
	if id < 1 {
		return Team{}, ErrRecordNotFound
	}

	for _, team := range s.teams {
		if team.ID == id {
			return team, nil
		}
	}

	return Team{}, ErrRecordNotFound
}

func (s *TeamStore) Insert(team Team) (Team, error) {
	team.ID = len(s.teams) + 1
	team.CreatedAt = time.Now().UTC()
	team.Version = 1

	s.teams = append(s.teams, team)
	return team, nil
}
