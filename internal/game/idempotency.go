package game

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"lt-api.aleksrdvn.com/internal/validator"
)

// ErrIdempotencyConflict rejects a request whose Idempotency-Key was already
// used with a different request (hash mismatch) — key misuse, never a retry.
var ErrIdempotencyConflict = errors.New("idempotency key conflict")

// IdempotencyToken carries the HTTP request's dedup identity:
//   - Key: the client's Idempotency-Key header, one value per logical request
//   - Endpoint: the route pattern, for ops visibility
//   - Hash: SHA-256 over method + "\n" + request URI (+ body when present) —
//     identical requests share it, a re-used key with a different request does not
type IdempotencyToken struct {
	Key      string
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

// RideMarshal is the presentation callback a ride command hands its result
// to: it returns the status and marshaled body the command stores as its
// replayable response (ADR-024).
type RideMarshal func(Ride) (int, []byte, error)

// IdempotencyStore owns the idempotency_keys table. Every method runs on a
// caller-owned transaction, because the claim shares the command's
// transaction:
//   - a failed command rolls its claim back — failures are never cached
//   - a concurrent duplicate blocks on the claim, then replays the committed
//     response (single-flight by insert conflict, no in-flight state machine)
type IdempotencyStore struct{}

// Claim tries to claim the key inside tx:
//   - claimed=true: this request owns the key and must execute the command
//   - claimed=false: the key is already completed — replay is its stored answer
//   - ErrIdempotencyConflict: the key exists with a different request hash
func (s IdempotencyStore) Claim(ctx context.Context, tx pgx.Tx, token IdempotencyToken) (bool, IdempotentResponse, error) {
	claim := `
		INSERT INTO idempotency_keys (key, endpoint, request_hash, response_status, response_body)
		VALUES ($1, $2, $3, 0, ''::bytea)
		ON CONFLICT (key) DO NOTHING
		RETURNING true
	`
	var claimed bool
	err := tx.QueryRow(ctx, claim, token.Key, token.Endpoint, token.Hash).Scan(&claimed)
	if err == nil {
		return true, IdempotentResponse{}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, IdempotentResponse{}, err
	}

	// Lost the claim: another request committed this key. Either it is the
	// same logical request (replay its stored answer) or the key is misused.
	var stored IdempotentResponse
	var hash []byte
	err = tx.QueryRow(ctx, `
		SELECT request_hash, response_status, response_body
		FROM idempotency_keys
		WHERE key = $1
	`, token.Key).Scan(&hash, &stored.Status, &stored.Body)
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
func (s IdempotencyStore) Save(ctx context.Context, tx pgx.Tx, token IdempotencyToken, res IdempotentResponse) error {
	tag, err := tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET response_status = $2, response_body = $3
		WHERE key = $1
	`, token.Key, res.Status, res.Body)
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
