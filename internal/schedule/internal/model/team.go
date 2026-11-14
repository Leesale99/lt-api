package model

import (
	"net/url"
	"path"
	"strings"
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

type Team struct {
	ID          int
	CreatedAt   time.Time
	Name        string
	Logo        string
	Description string
	Version     int
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
