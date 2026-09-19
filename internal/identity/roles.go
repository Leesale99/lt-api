package identity

import "github.com/jackc/pgx/v5/pgxpool"

type Role struct {
	ID   int
	Name string
}

type RoleStore struct {
	pool *pgxpool.Pool
}
