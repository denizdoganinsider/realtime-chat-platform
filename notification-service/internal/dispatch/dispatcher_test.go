package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/service"
)

type stubResolver struct{ recipients []service.Recipient }

func (s stubResolver) Recipients(context.Context, domain.MessageEvent) ([]service.Recipient, error) {
	return s.recipients, nil
}

// blockingDeliverer never returns until released - a receiver that hangs.
type blockingDeliverer struct {
	release   chan struct{}
	mu        sync.Mutex
	delivered int
}

func (d *blockingDeliverer) Deliver(ctx context.Context, r service.Recipient, e domain.MessageEvent) {
	select {
	case <-d.release:
	case <-ctx.Done():
	}
	d.mu.Lock()
	d.delivered++
	d.mu.Unlock()
}

func (d *blockingDeliverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.delivered
}

func recipients(n int) []service.Recipient {
	out := make([]service.Recipient, n)
	for i := range n {
		out[i] = service.Recipient{Webhook: domain.Webhook{UserID: int64(i + 1)}, Delivery: domain.Delivery{ID: int64(i + 1)}}
	}
	return out
}

// The request path must not wait on receivers: Enqueue returns at once even
// when every delivery worker is stuck.
func TestEnqueueNeverBlocksOnSlowReceivers(t *testing.T) {
	deliverer := &blockingDeliverer{release: make(chan struct{})}
	d := NewDispatcher(stubResolver{recipients: recipients(3)}, deliverer, 2)
	defer func() {
		close(deliverer.release)
		d.Close(time.Second)
	}()

	done := make(chan struct{})
	go func() {
		for range 50 {
			d.Enqueue(domain.MessageEvent{EventID: "e", Room: "general", UserID: 1, Content: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Enqueue blocked behind stuck delivery workers")
	}
}

func TestDeliveriesReachEveryRecipient(t *testing.T) {
	deliverer := &blockingDeliverer{release: make(chan struct{})}
	close(deliverer.release) // never blocks
	d := NewDispatcher(stubResolver{recipients: recipients(5)}, deliverer, 3)
	defer d.Close(time.Second)

	d.Enqueue(domain.MessageEvent{EventID: "e", Room: "general", UserID: 1, Content: "x"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if deliverer.count() == 5 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("delivered %d of 5", deliverer.count())
}

// Close cancels in-flight deliveries: a worker stuck in a receiver call or a
// backoff sleep returns now, not after the receiver does.
func TestCloseCancelsInFlightDeliveries(t *testing.T) {
	deliverer := &blockingDeliverer{release: make(chan struct{})}
	d := NewDispatcher(stubResolver{recipients: recipients(2)}, deliverer, 2)

	d.Enqueue(domain.MessageEvent{EventID: "e", Room: "general", UserID: 1, Content: "x"})
	time.Sleep(50 * time.Millisecond) // let the workers pick the jobs up

	start := time.Now()
	d.Close(2 * time.Second)

	if time.Since(start) > time.Second {
		t.Errorf("Close took %v; in-flight deliveries were not cancelled", time.Since(start))
	}
}
