package model

import (
	"crypto/rand"
	"crypto/sha256"
	"time"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

const (
	ScopeActivation     = "activation"
	ScopeAuthentication = "authentication"
)

// plaintextTokenLength is the fixed length of crypto/rand.Text() output —
// a 128-bit value, base32-encoded. Named (not inlined) so the generator and
// the validator can't silently drift apart.
const plaintextTokenLength = 26

// UserToken is one token row: the plaintext exists only in memory (returned
// to the caller that must deliver it), the hash is what storage keeps.
type UserToken struct {
	Plaintext string
	Hash      []byte
	UserID    int
	Expiry    time.Time
	Scope     string
}

// GenerateUserToken mints a token: random plaintext, its SHA-256 hash, the
// ttl-derived expiry and the scope. Pure domain logic — the repository only
// persists the result.
func GenerateUserToken(userID int, ttl time.Duration, scope string) UserToken {
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
