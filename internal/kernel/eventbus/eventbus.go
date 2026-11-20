// Package eventbus is transverse infrastructure: a minimal in-process,
// synchronous pub/sub used as the module event seam of the Game/Schedule
// split. It imports no domain package — events travel as `any`
// and every subscriber type-asserts — so neither game nor schedule can leak
// through it and no shared events package is needed: each publisher defines
// its events in its own package (ADR-027: events speak the publisher's
// language).
//
// The delivery contract is post-commit, in-process, synchronous:
//
//	Publisher: commit its transaction, THEN Publish.
//	Subscriber: handle synchronously; the publisher's call returns only
//	            after every handler returned.
//
// There is no persistence, retry or at-least-once guarantee. A handler that
// fails (or a crash between commit and publish) drops the event on the
// floor — the accepted gap of this phase, closed by the Phase 07 sweep /
// Phase 08 transactional outbox. Subscribers must therefore be idempotent
// (they claim their event ID before writing), so a future delivery
// mechanism can replay freely.
package eventbus

import (
	"context"
	"slices"
	"sync"
)

// Handler is one subscriber. The event arrives as `any`; handlers that care
// about one type should subscribe through the generic Subscribe helper.
type Handler func(ctx context.Context, event any) error

// Bus is the in-process exchange. Subscribe and Publish are safe for
// concurrent use (composition roots wire once at startup, but tests may
// subscribe while another goroutine publishes).
type Bus struct {
	mu       sync.RWMutex
	handlers []Handler
}

// New returns an empty bus.
func New() *Bus { return &Bus{} }

// Subscribe appends a handler. Delivery order is registration order; a
// subscriber must not rely on ordering relative to other subscribers.
// rule:R-KERNEL-013
func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// Subscribe registers a typed handler: only events of type T reach it,
// everything else passes through. The type parameter is the seam's convenience —
// the bus itself stays domain-free.
func Subscribe[T any](b *Bus, handle func(ctx context.Context, event T) error) {
	b.Subscribe(func(ctx context.Context, event any) error {
		typed, ok := event.(T)
		if !ok {
			return nil
		}
		return handle(ctx, typed)
	})
}

// Publish delivers the event to every subscriber synchronously, in
// registration order, and returns the first handler error. Callers publish
// only after their transaction committed (post-commit contract): the event
// is already durable-side-effect territory, so a handler failure must not
// roll anything back — the publisher decides how to surface it (log the
// gap; the sweep will settle it).
// rule:R-KERNEL-013
func (b *Bus) Publish(ctx context.Context, event any) error {
	b.mu.RLock()
	handlers := slices.Clone(b.handlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		if err := h(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
