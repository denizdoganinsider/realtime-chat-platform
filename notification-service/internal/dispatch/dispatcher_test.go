package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/service"
)

type stubResolver struct {
	recipients []service.Recipient
	mu         sync.Mutex
	calls      int
}

func (s *stubResolver) Recipients(context.Context, domain.MessageEvent) ([]service.Recipient, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.recipients, nil
}

func (s *stubResolver) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
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
	d := NewDispatcher(&stubResolver{recipients: recipients(3)}, deliverer, 2)
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
	d := NewDispatcher(&stubResolver{recipients: recipients(5)}, deliverer, 3)
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

// Past the drain budget, Close cancels in-flight deliveries: a worker stuck in
// a receiver call or a backoff sleep returns then, not when the receiver does.
func TestCloseCancelsInFlightDeliveriesAfterTimeout(t *testing.T) {
	deliverer := &blockingDeliverer{release: make(chan struct{})}
	d := NewDispatcher(&stubResolver{recipients: recipients(2)}, deliverer, 2)

	d.Enqueue(domain.MessageEvent{EventID: "e", Room: "general", UserID: 1, Content: "x"})
	time.Sleep(50 * time.Millisecond) // let the workers pick the jobs up

	start := time.Now()
	d.Close(200 * time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Close took %v; in-flight deliveries were not cancelled", elapsed)
	}
}

// Events that were answered 202 but not yet fanned out when SIGTERM arrives
// still get their fan-out (and so their delivery rows) during the drain.
func TestCloseDrainsQueuedEvents(t *testing.T) {
	resolver := &stubResolver{recipients: recipients(1)}
	deliverer := &blockingDeliverer{release: make(chan struct{})}
	close(deliverer.release)

	// Zero delivery workers is not a real configuration, but one fan-out
	// worker that we never let run before Close is: fill the queue while the
	// worker is busy on a first event that blocks.
	gate := make(chan struct{})
	blockingResolver := &gatedResolver{inner: resolver, gate: gate}
	d := NewDispatcher(blockingResolver, deliverer, 1)

	for range 5 {
		d.Enqueue(domain.MessageEvent{EventID: "e", Room: "general", UserID: 1, Content: "x"})
	}
	time.Sleep(20 * time.Millisecond) // the worker is now blocked on event 1
	close(gate)
	d.Close(2 * time.Second)

	if resolver.count() != 5 {
		t.Errorf("fan-out ran for %d of 5 accepted events", resolver.count())
	}
}

type gatedResolver struct {
	inner *stubResolver
	gate  chan struct{}
}

func (g *gatedResolver) Recipients(ctx context.Context, e domain.MessageEvent) ([]service.Recipient, error) {
	<-g.gate
	return g.inner.Recipients(ctx, e)
}
