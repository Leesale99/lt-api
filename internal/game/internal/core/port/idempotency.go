package port

// The idempotency seam (ADR-024). The claim-first single-flight engine lives
// in internal/kernel/idempotency, but the core speaks
// its own vocabulary: Token and SavedResponse below. The composition
// boundary (the module facade) converts them, so no idempotency type leaks
// into the core — the field layouts are identical, which keeps the
// conversion a plain type conversion.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Token is the request's dedup identity:
//   - Key: the client's Idempotency-Key header, one value per logical request
//   - UserID: the authenticated caller the key belongs to — the claim row is
//     (key, user_id), so two callers that pick the same key hold two claims.
//     Zero is the system caller (settlement events, no request behind them).
//   - Endpoint: the route pattern, for ops visibility
//   - Hash: SHA-256 over method + request URI (+ body when present) —
//     identical requests share it, a re-used key with a different request
//     does not
type Token struct {
	Key      string
	UserID   int
	Endpoint string
	Hash     []byte
}

// SavedResponse is the stored HTTP answer a replay returns byte-identical:
// status and marshaled body are the transport's presentation choice,
// persisted inside the command's transaction (ADR-024).
type SavedResponse struct {
	Status int
	Body   []byte
}

// Idempotency owns the claim table, on the caller's transaction:
//   - Claim: claimed=true means this request owns the key and must execute
//     the command; claimed=false means the key is already completed and the
//     returned response is its stored answer
//   - Save: stores the executed command's answer before the same transaction
//     commits, so a failed command rolls its claim back (failures are never
//     cached)
type Idempotency interface {
	Claim(ctx context.Context, tx pgx.Tx, token Token) (bool, SavedResponse, error)
	Save(ctx context.Context, tx pgx.Tx, token Token, res SavedResponse) error
}
