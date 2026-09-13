package game

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx"
	"github.com/jackc/pgx/v5/pgxpool"
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

func ValidateTeam(v *validator.Validator, team Team) {
	v.Check(team.Name != "", "name", "must be provided")
	v.Check(len(team.Name) <= 500, "name", "must not be more than 500 bytes long")
	v.Check(team.Logo != "", "logo", "must be provided")
	if team.Logo != "" {
		v.Check(validImageURL(team.Logo), "logo", "must be a valid image URL ending in png, jpg, jpeg, svg or webp")
	}
	v.Check(team.Description != "", "description", "must be provided")
	v.Check(len(team.Description) <= 5000, "description", "must not be more than 5000 bytes long")
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
	pool *pgxpool.Pool
}

func (s *TeamStore) Get(id int) (Team, error) {
	if id < 1 {
		return Team{}, ErrRecordNotFound
	}

	query := `
			SELECT id, created_at, name, logo, description, version
			FROM teams
			WHERE id = $1
	`

	var team Team

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&team.ID,
		&team.CreatedAt,
		&team.Name,
		&team.Logo,
		&team.Description,
		&team.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return Team{}, ErrRecordNotFound
		default:
			return Team{}, err
		}
	}

	return team, nil
}

func (s *TeamStore) Insert(team Team) (Team, error) {
	query := `
			INSERT INTO teams (name, logo, description)
			VALUES ($1, $2, $3)
			RETURNING id, created_at, version
	`
	args := []any{team.Name, team.Logo, team.Description}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := s.pool.QueryRow(ctx, query, args...).Scan(&team.ID, &team.CreatedAt, &team.Version)

	return team, err
}
