package identity

// Module doc: the activation use case (ADR-025).
//
// The activation invariant — the flag flip and the activation-token
// cleanup are one unit of persistence — is owned by the store layer, in a
// use-case method on identity.Store. The method spans two stores
// (Users.UpdateTx, UserTokens.DeleteAllForUserTx), so no single member
// store can own it; Store holds the pool precisely so it can open the
// transaction itself.
//
// Why not the alternatives (full comparison in ADR-025):
//   - A single data-modifying CTE (UPDATE ... RETURNING feeding the
//     DELETE) is atomic per statement, but it folds the token JOIN, the
//     version guard and the cleanup into one SQL blob the Go layer cannot
//     unit-test statement by statement, and its error surface (0 rows
//     means unknown token OR stale version OR already-activated) needs a
//     classification query — the same complexity in a less inspectable
//     shape.
//   - Transaction choreography in the handler (DBTX + WithTx) was already
//     rejected in review: it leaks the store shape into the HTTP layer.
//
// Handler error mapping is preserved: ErrRecordNotFound is "invalid or
// expired activation token" (422), ErrEditConflict is the lost version
// race (409).

import (
	"context"
)

// Activate flips the user's activated flag and deletes their activation
// tokens in one transaction. tokenPlaintext is the raw token from the
// request body; the lookup hashes it (GetForTokenTx) and refuses unknown,
// expired or out-of-scope tokens with ErrRecordNotFound.
func (s *Store) Activate(ctx context.Context, tokenPlaintext string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	// The read runs inside the transaction: the activated flag and the
	// version the guarded write rechecks come from the same snapshot the
	// write lands on, so an intervening edit by another writer turns into
	// a clean ErrEditConflict instead of a silent overwrite.
	user, err := s.Users.GetForTokenTx(ctx, tx, ScopeActivation, tokenPlaintext)
	if err != nil {
		return User{}, err
	}

	user.Activated = true

	user, err = s.Users.UpdateTx(ctx, tx, user)
	if err != nil {
		return User{}, err
	}

	if err := s.UserTokens.DeleteAllForUserTx(ctx, tx, ScopeActivation, user.ID); err != nil {
		return User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}

	return user, nil
}
