package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"lt-api.aleksrdvn.com/internal/store"
	"lt-api.aleksrdvn.com/internal/validator"
)

var ErrDuplicateEmail = errors.New("duplicate email")

type User struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Password  password  `json:"-"`
	Activated bool      `json:"activated"`
	RoleID    int       `json:"role_id"`
	Version   int       `json:"-"`
}

type password struct {
	plaintext *string
	hash      []byte
}

func (p *password) Set(plaintextPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), 12)
	if err != nil {
		return err
	}

	p.plaintext = &plaintextPassword
	p.hash = hash

	return nil
}

func (p *password) Matches(plaintextPassword string) (bool, error) {
	err := bcrypt.CompareHashAndPassword(p.hash, []byte(plaintextPassword))
	if err != nil {
		switch {
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			return false, err
		}
	}

	return true, nil
}

func ValidateEmail(v *validator.Validator, email string) {
	v.Check(email != "", "email", "must be provided")
	v.Check(validator.Matches(email, validator.EmailRX), "email", "must be a valid email address")
}

func ValidatePasswordPlaintext(v *validator.Validator, password string) {
	v.Check(password != "", "password", "must be provided")
	v.Check(len(password) >= 8, "password", "must be at least 8 bytes long")
	v.Check(len(password) <= 72, "password", "must not be more then 72 bytes long")
}

// ValidateRegistration validates the client-supplied fields of a
// registration request BEFORE the password is hashed. bcrypt refuses input
// over 72 bytes with an error, so hashing first would surface that as a
// 500; validating the plaintext first turns it into a 422, and skips the
// bcrypt cost entirely for input that is about to be rejected. This is the
// only user validator: the hash-nil invariant lives in the store
// (Insert/Update), not here — validators collect field errors, they never
// panic.
func ValidateRegistration(v *validator.Validator, name, email, plaintextPassword string) {
	v.Check(name != "", "name", "must be provided")
	v.Check(len(name) <= 500, "name", "must not be more then 500 bytes long")
	ValidateEmail(v, email)
	ValidatePasswordPlaintext(v, plaintextPassword)
}

type UserStore struct {
	pool *pgxpool.Pool
}

func (s *UserStore) Insert(ctx context.Context, user User) (User, error) {
	// Invariant, not validation: a user can never be persisted without a
	// hash. The DB's NOT NULL is the last line of defense; this panic is the
	// fast, loud one at the call site.
	if user.Password.hash == nil {
		panic("missing password hash for the user")
	}

	query := `
		INSERT INTO users (name, email, password_hash, activated, role_id)
		VALUES ($1, $2, $3, $4, (SELECT id FROM roles WHERE name = 'user'))
		RETURNING id, created_at, role_id, version
	`
	args := []any{user.Name, user.Email, user.Password.hash, user.Activated}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&user.ID, &user.CreatedAt, &user.RoleID, &user.Version)
	if err != nil {
		switch {
		case store.IsUniqueViolation(err, "users_email_key"):
			return User{}, ErrDuplicateEmail
		default:
			return User{}, err
		}
	}

	return user, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (User, error) {
	query := `
		SELECT id, created_at, name, email, password_hash, activated, role_id, version
		FROM users
		WHERE email = $1
	`
	var user User

	err := s.pool.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.CreatedAt,
		&user.Name,
		&user.Email,
		&user.Password.hash,
		&user.Activated,
		&user.RoleID,
		&user.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return User{}, store.ErrRecordNotFound
		default:
			return User{}, err
		}
	}

	return user, nil
}

func (s *UserStore) Update(ctx context.Context, user User) (User, error) {
	if user.Password.hash == nil {
		panic("missing password hash for the user")
	}

	query := `
		UPDATE users
		SET name = $1, email = $2, password_hash = $3, activated = $4, version = version + 1
		WHERE id = $5 AND version = $6
		RETURNING role_id, version
	`
	args := []any{user.Name, user.Email, user.Password.hash, user.Activated, user.ID, user.Version}

	err := s.pool.QueryRow(ctx, query, args...).Scan(&user.RoleID, &user.Version)
	if err != nil {
		switch {
		case store.IsUniqueViolation(err, "users_email_key"):
			return User{}, ErrDuplicateEmail
		case errors.Is(err, pgx.ErrNoRows):
			return User{}, store.ErrEditConflict
		default:
			return User{}, err
		}
	}

	return user, nil
}

func (s *UserStore) GetForToken(ctx context.Context, tokenScope, tokenPlanetext string) (User, error) {
	tokenHash := sha256.Sum256([]byte(tokenPlanetext))

	query := `
		SELECT users.id, users.created_at, users.name, users.email, users.password_hash, users.activated, users.role_id, users.version
		FROM users
		INNER JOIN user_tokens
		ON users.id = user_tokens.user_id
		WHERE user_tokens.hash = $1 AND user_tokens.scope = $2 AND user_tokens.expiry > $3
	`

	args := []any{tokenHash[:], tokenScope, time.Now()}

	var user User

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&user.ID,
		&user.CreatedAt,
		&user.Name,
		&user.Email,
		&user.Password.hash,
		&user.Activated,
		&user.RoleID,
		&user.Version,
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return User{}, store.ErrRecordNotFound
		default:
			return User{}, err
		}
	}

	return user, nil
}
