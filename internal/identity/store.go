package identity

import "github.com/jackc/pgx/v5/pgxpool"

// Store owns the pool so its use-case methods (see activation.go) can open
// transactions across member stores: a use case that spans several stores
// cannot be owned by any one of them.
type Store struct {
	pool        *pgxpool.Pool
	Users       UserStore
	UserTokens  UserTokenStore
	Permissions PermissionStore
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:        pool,
		Users:       UserStore{pool},
		UserTokens:  UserTokenStore{pool},
		Permissions: PermissionStore{pool},
	}
}
