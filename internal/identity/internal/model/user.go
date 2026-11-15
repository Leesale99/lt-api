package model

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

type User struct {
	ID        int
	CreatedAt time.Time
	Name      string
	Email     string
	Password  password
	Activated bool
	RoleID    int
	Version   int
}

// password keeps the plaintext (while the caller still holds it) and the
// bcrypt hash. Only the hash ever leaves the process; the type's fields are
// unexported so no layer can read or forge one directly.
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

// Hash returns the stored hash — the repository's write path (INSERT/UPDATE
// bind it) reads it through this accessor; the field itself never leaves the
// model's control.
func (p *password) Hash() []byte { return p.hash }

// SetHash installs a hash read back from storage. It is the repository's read
// path: the row carries the hash, the entity carries it opaquely.
func (p *password) SetHash(hash []byte) { p.hash = hash }

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
// only user validator: the hash-nil invariant lives in the repository
// (Insert/Update), not here — validators collect field errors, they never
// panic.
func ValidateRegistration(v *validator.Validator, name, email, plaintextPassword string) {
	v.Check(name != "", "name", "must be provided")
	v.Check(len(name) <= 500, "name", "must not be more then 500 bytes long")
	ValidateEmail(v, email)
	ValidatePasswordPlaintext(v, plaintextPassword)
}
