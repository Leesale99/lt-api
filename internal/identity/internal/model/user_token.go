package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"time"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

const (
	ScopeActivation     = "activation"
	ScopeAuthentication = "authentication"
)

const (
	// tokenEntropyBytes is the entropy every plaintext is minted from: 160
	// bits, of which the encoding's first plaintextTokenLength characters carry
	// 130 (a hundred and twenty-eight and a bit of slack).
	tokenEntropyBytes = 20

	// plaintextTokenLength is the fixed length of a minted token: RFC 4648
	// base32 of tokenEntropyBytes with no padding is 32 characters, and a
	// token keeps the first 26 of them. Named (not inlined) so the generator,
	// the trim and the validator share one home and cannot drift apart.
	plaintextTokenLength = 26
)

// UserToken is one token row: the plaintext exists only in memory (returned
// to the caller that must deliver it), the hash is what storage keeps.
type UserToken struct {
	Plaintext string
	Hash      []byte
	UserID    int
	Expiry    time.Time
	Scope     string
}

// newTokenPlaintext mints the plaintext here instead of delegating to
// crypto/rand.Text(): plaintextTokenLength is this package's contract — the
// validator refuses a credential of any other length — so generation must not
// inherit the standard library's documented output shape. A stdlib change to
// rand.Text's alphabet or letter count would otherwise silently make the
// validator reject freshly minted tokens (misalignment-report §6.20).
//
// The derivation is deliberately explicit: tokenEntropyBytes from crypto/rand,
// RFC 4648 base32 (upper-case A–Z and 2–7, no padding), the first
// plaintextTokenLength characters. That is the shape rand.Text produced.
func newTokenPlaintext() string {
	var entropy [tokenEntropyBytes]byte
	// crypto/rand.Read never returns an error (it panics on a broken source),
	// so a failure here cannot leave a predictable token behind.
	if _, err := rand.Read(entropy[:]); err != nil {
		panic("identity: cannot read a token's entropy: " + err.Error())
	}

	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:])

	return encoded[:plaintextTokenLength]
}

// GenerateUserToken mints a token: random plaintext, its SHA-256 hash, the
// ttl-derived expiry and the scope. Pure domain logic — the repository only
// persists the result.
func GenerateUserToken(userID int, ttl time.Duration, scope string) UserToken {
	userToken := UserToken{
		Plaintext: newTokenPlaintext(),
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
