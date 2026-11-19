package model

// TestMain lowers the bcrypt cost for the whole package. The only hashing
// here is the password round trip in user_test.go, which pins bcrypt's
// semantics — plaintext never stored, a match, a mismatch as (false, nil), a
// genuine error against an unset hash — none of which depend on how many
// rounds ran. So the suite pays bcrypt.MinCost instead of the production cost.

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	PasswordCost = bcrypt.MinCost
	os.Exit(m.Run())
}
