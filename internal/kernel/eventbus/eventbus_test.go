package eventbus

import (
	"context"
	"errors"
	"testing"
)

type testEvent struct{ N int }

func TestBus_SubscribeAndPublish(t *testing.T) {
	bus := New()

	var got []int
	Subscribe(bus, func(ctx context.Context, e testEvent) error {
		got = append(got, e.N)
		return nil
	})
	// A handler for another type must never see this event.
	Subscribe(bus, func(ctx context.Context, e struct{}) error {
		t.Error("wrong-typed handler received the event")
		return nil
	})

	if err := bus.Publish(context.Background(), testEvent{N: 7}); err != nil {
		t.Fatalf("Publish() = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("handler saw %v, want [7]", got)
	}
}

func TestBus_PublishReturnsFirstHandlerError(t *testing.T) {
	bus := New()
	boom := errors.New("boom")

	Subscribe(bus, func(ctx context.Context, e testEvent) error { return boom })
	Subscribe(bus, func(ctx context.Context, e testEvent) error {
		t.Error("second handler ran after the first failed")
		return nil
	})

	if err := bus.Publish(context.Background(), testEvent{}); !errors.Is(err, boom) {
		t.Fatalf("Publish() = %v, want boom", err)
	}
}

func TestBus_EmptyPublishIsNoOp(t *testing.T) {
	bus := New()
	if err := bus.Publish(context.Background(), testEvent{}); err != nil {
		t.Fatalf("Publish() on empty bus = %v, want nil", err)
	}
}
