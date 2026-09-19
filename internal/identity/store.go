package identity

import "github.com/jackc/pgx/v5/pgxpool"

type Store struct {
	Users       UserStore
	UserTokens  UserTokenStore
	Permissions PermissionStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Users:       UserStore{pool},
		UserTokens:  UserTokenStore{pool},
		Permissions: PermissionStore{pool},
	}
}
