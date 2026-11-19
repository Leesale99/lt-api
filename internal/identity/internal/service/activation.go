package service

// The activation use cases (ADR-025): registration mints the account and its
// activation token, the token request re-mints one for an unactivated user,
// and Activate flips the flag.
//
// The activation invariant — the flag flip and the activation-token cleanup
// are one unit of persistence — is owned by this layer, in Activate. The
// method spans two repositories (Users.UpdateTx, UserTokens.DeleteAllForUserTx),
// so no single repository can own it; the service holds the pool precisely so
// it can open the transaction itself.
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
//     rejected in review: it leaks the storage shape into the HTTP layer.
//
// Handler error mapping is preserved: model.ErrNotFound is "invalid or
// expired activation token" (422), model.ErrEditConflict is the lost version
// race (409).

import (
	"context"
	"errors"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// Mailer is everything identity needs from the outside world's mail
// transport: one method, exactly the shape internal/platform/mailer.Send has —
// the concrete SMTP client satisfies it structurally, and a test injects a
// no-op instead of a live client. Declared here, at the consumer, so the
// module never depends on SMTP.
type Mailer interface {
	Send(ctx context.Context, recipient string, templateFile string, data any) error
}

// Register creates the account and its activation token, and books the
// welcome email. The caller has already validated the request (validating
// before hashing is what turns an over-72-byte password into a 422 instead
// of a bcrypt error surfacing as a 500).
//
// Timeout placement: the password hash runs FIRST, untimed (still bounded by
// the request context), and only the two store writes share one
// persistence.DBTimeout window. The budget bounds database work, not crypto —
// a cost-12 hash can eat it on a contended machine and starve the INSERT.
// The mail dispatch stays outside the timed section: it is not storage.
func (s *Service) Register(ctx context.Context, user model.User, plaintextPassword string) (model.User, error) {
	if err := user.Password.Set(plaintextPassword); err != nil {
		return model.User{}, err
	}

	user.Activated = false

	dbCtx, cancel := context.WithTimeout(ctx, persistence.DBTimeout)
	defer cancel()

	user, err := s.users.Insert(dbCtx, user)
	if err != nil {
		return model.User{}, err
	}

	userToken, err := s.userTokens.New(dbCtx, user.ID, activationTokenTTL, model.ScopeActivation)
	if err != nil {
		return model.User{}, err
	}

	s.dispatch("user_welcome.tmpl", user.Email, map[string]any{
		"name":            user.Name,
		"activationToken": userToken.Plaintext,
	})

	return user, nil
}

// RequestActivationToken mints a fresh activation token for an unactivated
// user and books the email that carries it. model.ErrNotFound is an unknown
// email; model.ErrAlreadyActivated is an already-active account. Both are
// verdicts for the caller's own bookkeeping, not refusal shapes: no token is
// minted and no mail is booked for either, and the handler answers both with
// the same 202 envelope a genuine request receives, so the endpoint tells a
// caller nothing about which addresses exist (R-IDENTITY-022).
func (s *Service) RequestActivationToken(ctx context.Context, email string) error {
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		return err
	}

	if user.Activated {
		return model.ErrAlreadyActivated
	}

	userToken, err := s.userTokens.New(ctx, user.ID, activationTokenTTL, model.ScopeActivation)
	if err != nil {
		return err
	}

	s.dispatch("user_token_activation.tmpl", user.Email, map[string]any{
		"activationToken": userToken.Plaintext,
	})

	return nil
}

// Authenticate checks the password and mints an authentication token. An
// unknown email and a wrong password both surface as
// model.ErrInvalidCredentials: the same 401 from the same code path — the
// caller's response must not reveal which half failed, in what it says or in
// how long it takes to say it.
//
// Timeout placement: each store call gets its own persistence.DBTimeout
// child; the bcrypt compare runs BETWEEN them, outside any DB budget, so a
// slow hash cannot turn a healthy token INSERT into a deadline error.
func (s *Service) Authenticate(ctx context.Context, email, plaintextPassword string) (model.UserToken, error) {
	lookupCtx, cancelLookup := context.WithTimeout(ctx, persistence.DBTimeout)
	defer cancelLookup()

	user, err := s.users.GetByEmail(lookupCtx, email)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			// Timing equivalence (R-IDENTITY-018). The wrong-password path
			// below spends a bcrypt comparison before it refuses; if this
			// path returned here, the two "invalid credentials" answers
			// would differ by exactly that work, and a caller who times
			// sign-in could enumerate registered addresses. So burn one
			// comparison against a constant hash and throw the verdict
			// away. The connection is released first, as it is on the
			// real path, so both paths hold a DB connection for the same
			// part of the request and spend the same time outside the
			// budget.
			cancelLookup()
			model.BurnPasswordComparison(plaintextPassword)
			return model.UserToken{}, model.ErrInvalidCredentials
		}
		return model.UserToken{}, err
	}

	cancelLookup()

	// Untimed: bounded by the request context only, never by the DB budget.
	match, err := user.Password.Matches(plaintextPassword)
	if err != nil {
		return model.UserToken{}, err
	}
	if !match {
		return model.UserToken{}, model.ErrInvalidCredentials
	}

	tokenCtx, cancelToken := context.WithTimeout(ctx, persistence.DBTimeout)
	defer cancelToken()

	userToken, err := s.userTokens.New(tokenCtx, user.ID, authenticationTokenTTL, model.ScopeAuthentication)
	if err != nil {
		return model.UserToken{}, err
	}

	return userToken, nil
}

// Activate flips the user's activated flag and deletes their activation
// tokens in one transaction. tokenPlaintext is the raw token from the
// request body; the lookup hashes it (GetForTokenTx) and refuses unknown,
// expired or out-of-scope tokens with model.ErrNotFound.
func (s *Service) Activate(ctx context.Context, tokenPlaintext string) (model.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.User{}, err
	}

	defer func() { _ = tx.Rollback(context.Background()) }()

	// The read runs inside the transaction: the activated flag and the
	// version the guarded write rechecks come from the same snapshot the
	// write lands on, so an intervening edit by another writer turns into
	// a clean model.ErrEditConflict instead of a silent overwrite.
	user, err := s.users.GetForTokenTx(ctx, tx, model.ScopeActivation, tokenPlaintext)
	if err != nil {
		return model.User{}, err
	}

	user.Activated = true

	user, err = s.users.UpdateTx(ctx, tx, user)
	if err != nil {
		return model.User{}, err
	}

	if err := s.userTokens.DeleteAllForUserTx(ctx, tx, model.ScopeActivation, user.ID); err != nil {
		return model.User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.User{}, err
	}

	return user, nil
}

// dispatch books one email delivery. The tracked runner (when installed)
// hands it the process root context, not the request's: by the time the send
// runs the response may already have gone out. Without a runner the delivery
// happens inline on a fresh background context — untracked, but never
// dropped.
func (s *Service) dispatch(templateFile, recipient string, data any) {
	send := func(ctx context.Context) {
		if err := s.mailer.Send(ctx, recipient, templateFile, data); err != nil && s.log != nil {
			s.log.Error(err.Error())
		}
	}

	if s.background != nil {
		s.background(send)
		return
	}

	send(context.Background())
}
