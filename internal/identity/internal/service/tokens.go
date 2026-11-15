package service

import "time"

// Token lifetimes owned by the identity use cases: activation tokens live for
// days (the user must find the mail), authentication tokens for a day. Both
// are minted only here (Register, token request, login), so the values stay
// unexported and cannot drift from their use sites.
const (
	activationTokenTTL     = 3 * 24 * time.Hour
	authenticationTokenTTL = 24 * time.Hour
)
