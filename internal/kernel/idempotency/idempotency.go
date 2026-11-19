// Package idempotency owns the idempotency_keys table: the claim-first
// single-flight engine every idempotent command shares (ADR-024). It is a
// kernel package: a context-agnostic technical capability that owns no
// business rules and never imports a module, so any context that needs it may
// import it (game and schedule today; more than one is a signal, not a
// requirement). It is the kernel's first STATEFUL package — the
// idempotency_keys table is its state.
//
// The table's namespace is one caller's own: a claim row is (key, user_id), so
// two callers that pick the same key hold two independent claims (R-KERNEL-003,
// migration 000022). A claim is remembered for retentionWindow and no longer —
// the claim sweeps its stale rows on its own transaction, because a real
// background sweeper belongs to the Phase 07 worker (R-PLATFORM-016).
package idempotency

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// ErrIdempotencyConflict rejects a request whose Idempotency-Key was already
// used with a different request (hash mismatch) — key misuse, never a retry.
var ErrIdempotencyConflict = errors.New("idempotency key conflict")

// SystemCallerID is the claim namespace of work no request asked for: the
// settlement handlers claim an EventID with no principal behind it. It is not
// an identity id — users.id is serial, so no account can ever be 0.
const SystemCallerID = 0

// retentionWindow is how long a claim is remembered (R-KERNEL-003): a key
// whose claim is older than this is forgotten, so a command sent under it
// executes again instead of replaying. The claim sweeps expired rows lazily on
// its own transaction — a real background sweeper is the Phase 07 worker's job
// (R-PLATFORM-016 is still specified-only), so the window is a constant here,
// not configuration, until the worker owns it.
const retentionWindow = 24 * time.Hour

// IdempotencyToken carries the HTTP request's dedup identity:
//   - Key: the client's Idempotency-Key header, one value per logical request
//   - UserID: the authenticated caller the key belongs to (SystemCallerID for
//     work with no request behind it) — the claim row is (key, user_id), so two
//     callers that pick the same key hold two independent claims
//   - Endpoint: the route pattern, for ops visibility
//   - Hash: SHA-256 over method + "\n" + request URI (+ body when present) —
//     identical requests share it, a re-used key with a different request does not
type IdempotencyToken struct {
	Key      string
	UserID   int
	Endpoint string
	Hash     []byte
}

// ValidateIdempotencyToken checks the token's key: it is required (it
// identifies the logical request) and bounded by the idempotency_keys_key_check
// limit — the same rule the DB CHECK enforces as the backstop.
func ValidateIdempotencyToken(v *validator.Validator, token IdempotencyToken) {
	v.Check(token.Key != "", "idempotency_key", "must be provided")
	v.Check(len(token.Key) <= 255, "idempotency_key", "must not be more than 255 bytes")
}

// IdempotentResponse is the stored HTTP answer a replay returns
// byte-identical: status and marshaled body are the handler's presentation
// choice, persisted inside the command's transaction (ADR-024). Headers are
// not persisted — anything derivable (create's Location) is derived from
// the body by the handler, on the fresh path and the replay alike.
type IdempotentResponse struct {
	Status int
	Body   []byte
}

// Repository owns the idempotency_keys table. Every method runs on a
// caller-owned transaction, because the claim shares the command's
// transaction:
//   - a failed command rolls its claim back — failures are never cached
//   - a concurrent duplicate blocks on the claim, then replays the committed
//     response (single-flight by insert conflict, no in-flight state machine)
type Repository struct{}

// New returns the idempotency engine. It carries no state — the claim lives
// in the table, on the caller's transaction — so the zero value is usable
// too; the constructor exists for wiring symmetry with the other contexts.
func New() Repository { return Repository{} }

// Claim tries to claim the key for its caller inside tx:
//   - claimed=true: this request owns the key and must execute the command
//   - claimed=false: the key is already completed — replay is its stored answer
//   - ErrIdempotencyConflict: the key exists with a different request hash
//
// The namespace is (key, user_id), so a key another caller already used does
// not collide with this one: the INSERT simply succeeds under this caller's
// row. Before it, the claim sweeps rows older than retentionWindow — the lazy
// retention policy, one statement per claim, on the same transaction.
func (r Repository) Claim(ctx context.Context, tx pgx.Tx, token IdempotencyToken) (bool, IdempotentResponse, error) {
	if _, err := tx.Exec(ctx, `
		DELETE FROM idempotency_keys
		WHERE created_at < now() - make_interval(secs => $1)
	`, int(retentionWindow.Seconds())); err != nil {
		return false, IdempotentResponse{}, err
	}

	claim := `
		INSERT INTO idempotency_keys (key, user_id, endpoint, request_hash, response_status, response_body)
		VALUES ($1, $2, $3, $4, 0, ''::bytea)
		ON CONFLICT (key, user_id) DO NOTHING
		RETURNING true
	`
	var claimed bool
	err := tx.QueryRow(ctx, claim, token.Key, token.UserID, token.Endpoint, token.Hash).Scan(&claimed)
	if err == nil {
		return true, IdempotentResponse{}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, IdempotentResponse{}, err
	}

	// Lost the claim: another request for this caller committed the key. Either
	// it is the same logical request (replay its stored answer) or the key is
	// misused.
	var stored IdempotentResponse
	var hash []byte
	err = tx.QueryRow(ctx, `
		SELECT request_hash, response_status, response_body
		FROM idempotency_keys
		WHERE key = $1 AND user_id = $2
	`, token.Key, token.UserID).Scan(&hash, &stored.Status, &stored.Body)
	if err != nil {
		// Defensive: the INSERT blocks on the conflicting transaction, so a
		// row exists by the time DO NOTHING resolves. Missing would be an
		// unexpected state — refuse rather than silently re-execute.
		return false, IdempotentResponse{}, ErrIdempotencyConflict
	}
	if !bytes.Equal(hash, token.Hash) {
		return false, IdempotentResponse{}, ErrIdempotencyConflict
	}

	return false, stored, nil
}

// Save stores the command's HTTP answer on the claimed row. Runs in the
// command's transaction, so the claim commits only with its response.
func (r Repository) Save(ctx context.Context, tx pgx.Tx, token IdempotencyToken, res IdempotentResponse) error {
	tag, err := tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET response_status = $3, response_body = $4
		WHERE key = $1 AND user_id = $2
	`, token.Key, token.UserID, res.Status, res.Body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		// Save follows Claim in the same transaction; a missing claim row is
		// an app bug, surfaced as a conflict instead of a silent gap.
		return ErrIdempotencyConflict
	}

	return nil
}
