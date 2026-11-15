package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

// UserTokenRepository owns the user_tokens table.
type UserTokenRepository struct {
	pool *pgxpool.Pool
}

func NewUserTokenRepository(pool *pgxpool.Pool) *UserTokenRepository {
	return &UserTokenRepository{pool: pool}
}

// New mints and persists a token for the user.
func (r *UserTokenRepository) New(ctx context.Context, userID int, ttl time.Duration, scope string) (model.UserToken, error) {
	userToken := model.GenerateUserToken(userID, ttl, scope)

	if err := r.insert(ctx, userToken); err != nil {
		return model.UserToken{}, err
	}

	return userToken, nil
}

func (r *UserTokenRepository) insert(ctx context.Context, userToken model.UserToken) error {
	query := `
		INSERT INTO user_tokens (hash, user_id, expiry, scope)
		VALUES ($1, $2, $3, $4)
	`
	args := []any{userToken.Hash, userToken.UserID, userToken.Expiry, userToken.Scope}

	_, err := r.pool.Exec(ctx, query, args...)

	return err
}

func (r *UserTokenRepository) DeleteAllForUser(ctx context.Context, scope string, userID int) error {
	query := `
		DELETE FROM user_tokens
		WHERE scope = $1 AND user_id = $2
	`
	_, err := r.pool.Exec(ctx, query, scope, userID)
	return err
}

// DeleteAllForUserTx is DeleteAllForUser on a caller's transaction: the
// activation use case runs it beside the activated flip so the cleanup is
// one commit with the flag it cleans up after.
func (r *UserTokenRepository) DeleteAllForUserTx(ctx context.Context, tx pgx.Tx, scope string, userID int) error {
	query := `
		DELETE FROM user_tokens
		WHERE scope = $1 AND user_id = $2
	`
	_, err := tx.Exec(ctx, query, scope, userID)
	return err
}
