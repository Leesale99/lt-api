package identity

import "github.com/jackc/pgx/v5/pgxpool"

type Store struct {
	Users UsersStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Users: UsersStore{pool},
	}
}
