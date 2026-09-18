package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lt-api.aleksrdvn.com/internal/validator"
)

const (
	ScopeActivation     = "activation"
	ScopeAuthentication = "authentication"
)

// plaintextTokenLength is the fixed length of crypto/rand.Text() output —
// a 128-bit value, base32-encoded. Named (not inlined) so the generator and
// the validator can't silently drift apart.
const plaintextTokenLength = 26

type UserToken struct {
	Plaintext string    `json:"user_token"`
	Hash      []byte    `json:"-"`
	UserID    int       `json:"-"`
	Expiry    time.Time `json:"expiry"`
	Scope     string    `json:"-"`
}

func generateUserToken(userID int, ttl time.Duration, scope string) UserToken {
	userToken := UserToken{
		Plaintext: rand.Text(),
		UserID:    userID,
		Expiry:    time.Now().Add(ttl),
		Scope:     scope,
	}

	hash := sha256.Sum256([]byte(userToken.Plaintext))
	userToken.Hash = hash[:]

	return userToken
}

func ValidateUserTokenPlaintext(v *validator.Validator, userTokenPlaintext string) {
	v.Check(userTokenPlaintext != "", "user_token", "must be provided")
	v.Check(len(userTokenPlaintext) == plaintextTokenLength, "user_token", "must be 26 bytes long")
}

type UserTokenStore struct {
	pool *pgxpool.Pool
}

func (s *UserTokenStore) New(ctx context.Context, userID int, ttl time.Duration, scope string) (UserToken, error) {
	userToken := generateUserToken(userID, ttl, scope)

	err := s.insert(ctx, userToken)

	return userToken, err
}

func (s *UserTokenStore) insert(ctx context.Context, userToken UserToken) error {
	query := `
		INSERT INTO user_tokens (hash, user_id, expiry, scope)
		VALUES ($1, $2, $3, $4)
	`
	args := []any{userToken.Hash, userToken.UserID, userToken.Expiry, userToken.Scope}

	_, err := s.pool.Exec(ctx, query, args...)

	return err
}

func (s *UserTokenStore) DeleteAllForUser(ctx context.Context, scope string, userID int) error {
	query := `
		DELETE FROM user_tokens
		WHERE scope = $1 AND user_id = $2
	`
	_, err := s.pool.Exec(ctx, query, scope, userID)
	return err
}
