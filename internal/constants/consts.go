package constants

import "time"

const DBTimeout = 3 * time.Second

const (
	DefaultPage     = 1
	DefaultPageSize = 20
)

const UserTokenTTL = 3 * 24 * time.Hour
