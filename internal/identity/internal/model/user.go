package model

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// passwordHashCost is the bcrypt cost every stored password hash is minted
// with, and the cost the timing-equalizer hash below is minted with too: the
// dummy comparison has to cost what a real one costs, so both read this one
// constant.
const passwordHashCost = 12

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
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), passwordHashCost)
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

// dummyPasswordHash is a bcrypt hash of a constant this package never reveals,
// minted once per process (lazily, on the first burn) at passwordHashCost so a
// burn costs exactly what a real comparison costs. Its plaintext is irrelevant:
// nothing ever matches it, and the verdict is thrown away.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("identity-timing-equalizer"), passwordHashCost)
	if err != nil {
		panic("identity: cannot mint the timing-equalizer hash: " + err.Error())
	}
	return hash
})

// BurnPasswordComparison spends a bcrypt comparison against the package's
// constant dummy hash and discards the verdict. It exists so a refusal that
// has no stored hash to compare against (an unknown email at sign-in) can be
// made to cost the same work as one that does (a wrong password): the two
// "invalid credentials" answers must not be distinguishable by latency, or a
// caller can time sign-in to learn whether an address is registered
// (R-IDENTITY-018). Two independent users of it buy the cost twice, which is
// what a real comparison would cost them too.
func BurnPasswordComparison(plaintextPassword string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(plaintextPassword))
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
