package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// UserRepository owns the users table.
type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Insert(ctx context.Context, user model.User) (model.User, error) {
	// Invariant, not validation: a user can never be persisted without a
	// hash. The DB's NOT NULL is the last line of defense; this panic is the
	// fast, loud one at the call site.
	if user.Password.Hash() == nil {
		panic("missing password hash for the user")
	}

	query := `
		INSERT INTO users (name, email, password_hash, activated, role_id)
		VALUES ($1, $2, $3, $4, (SELECT id FROM roles WHERE name = 'user'))
		RETURNING id, created_at, role_id, version
	`
	args := []any{user.Name, user.Email, user.Password.Hash(), user.Activated}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&user.ID, &user.CreatedAt, &user.RoleID, &user.Version)
	if err != nil {
		switch {
		case persistence.IsUniqueViolation(err, "users_email_key"):
			return model.User{}, model.ErrDuplicateEmail
		default:
			return model.User{}, err
		}
	}

	return user, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (model.User, error) {
	query := `
		SELECT id, created_at, name, email, password_hash, activated, role_id, version
		FROM users
		WHERE email = $1
	`

	user, err := scanUser(r.pool.QueryRow(ctx, query, email))
	if err != nil {
		return model.User{}, classifyRead(err)
	}

	return user, nil
}

// Get returns the user by ID — the api-level user-existence check behind
// player create (000016: the players_user_id_fkey is gone with the
// cross-context FKs, so the api handler verifies the principal before
// writing the player; user_id is only ever the authenticated user's ID).
func (r *UserRepository) Get(ctx context.Context, id int) (model.User, error) {
	if id < 1 {
		return model.User{}, model.ErrNotFound
	}

	query := `
		SELECT id, created_at, name, email, password_hash, activated, role_id, version
		FROM users
		WHERE id = $1
	`

	user, err := scanUser(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return model.User{}, classifyRead(err)
	}

	return user, nil
}

func (r *UserRepository) Update(ctx context.Context, user model.User) (model.User, error) {
	return r.update(ctx, r.pool, user)
}

// rowQuerier is the common shape of pgxpool.Pool and pgx.Tx: both answer
// QueryRow with a pgx.Row. Naming it (rather than overloading) keeps the
// pool and tx variants of a read from drifting apart.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UpdateTx is Update on a caller's transaction: same guarded write, same
// error mapping (unique violation 23505, 0 rows is the lost version race).
// The activation use case runs it beside the token cleanup so the flip and
// the token deletion are one commit.
func (r *UserRepository) UpdateTx(ctx context.Context, tx pgx.Tx, user model.User) (model.User, error) {
	return r.update(ctx, tx, user)
}

func (r *UserRepository) update(ctx context.Context, q rowQuerier, user model.User) (model.User, error) {
	if user.Password.Hash() == nil {
		panic("missing password hash for the user")
	}

	query := `
		UPDATE users
		SET name = $1, email = $2, password_hash = $3, activated = $4, version = version + 1
		WHERE id = $5 AND version = $6
		RETURNING role_id, version
	`
	args := []any{user.Name, user.Email, user.Password.Hash(), user.Activated, user.ID, user.Version}

	err := q.QueryRow(ctx, query, args...).Scan(&user.RoleID, &user.Version)
	if err != nil {
		switch {
		case persistence.IsUniqueViolation(err, "users_email_key"):
			return model.User{}, model.ErrDuplicateEmail
		case errors.Is(err, pgx.ErrNoRows):
			return model.User{}, model.ErrEditConflict
		default:
			return model.User{}, err
		}
	}

	return user, nil
}

func (r *UserRepository) GetForToken(ctx context.Context, tokenScope, tokenPlaintext string) (model.User, error) {
	return r.getForToken(ctx, r.pool, tokenScope, tokenPlaintext)
}

// GetForTokenTx is GetForToken on a caller's transaction: the activation
// use case reads the token-protected user in the same tx that will flip
// the flag and delete the tokens, so the guarded write re-checks against
// the same snapshot the read produced.
func (r *UserRepository) GetForTokenTx(ctx context.Context, tx pgx.Tx, tokenScope, tokenPlaintext string) (model.User, error) {
	return r.getForToken(ctx, tx, tokenScope, tokenPlaintext)
}

func (r *UserRepository) getForToken(ctx context.Context, q rowQuerier, tokenScope, tokenPlaintext string) (model.User, error) {
	tokenHash := sha256.Sum256([]byte(tokenPlaintext))

	query := `
		SELECT users.id, users.created_at, users.name, users.email, users.password_hash, users.activated, users.role_id, users.version
		FROM users
		INNER JOIN user_tokens
		ON users.id = user_tokens.user_id
		WHERE user_tokens.hash = $1 AND user_tokens.scope = $2 AND user_tokens.expiry > $3
	`

	args := []any{tokenHash[:], tokenScope, time.Now()}

	user, err := scanUser(q.QueryRow(ctx, query, args...))
	if err != nil {
		return model.User{}, classifyRead(err)
	}

	return user, nil
}

// scanUser maps one users row onto the model value; the hash crosses the
// boundary opaquely (SetHash) so the password type keeps its fields private.
func scanUser(row pgx.Row) (model.User, error) {
	var user model.User
	var hash []byte

	err := row.Scan(
		&user.ID,
		&user.CreatedAt,
		&user.Name,
		&user.Email,
		&hash,
		&user.Activated,
		&user.RoleID,
		&user.Version,
	)
	if err != nil {
		return model.User{}, err
	}

	user.Password.SetHash(hash)

	return user, nil
}

// classifyRead turns a read's storage miss into the identity sentinel.
func classifyRead(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}
