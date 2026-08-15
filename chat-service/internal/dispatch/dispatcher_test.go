package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
)

// blockingSender never returns until released, which is how a real
// presence-service outage looks from here: retries with backoff, seconds long.
type blockingSender struct {
	release chan struct{}
	mu      sync.Mutex
	events  []domain.PresenceEvent
}

func newBlockingSender() *blockingSender {
	return &blockingSender{release: make(chan struct{})}
}

func (s *blockingSender) SendEvent(ctx context.Context, event domain.PresenceEvent) error {
	<-s.release

	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)

	return nil
}

func (s *blockingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

type blockingCreator struct {
	release chan struct{}
	mu      sync.Mutex
	created int
}

func newBlockingCreator() *blockingCreator {
	return &blockingCreator{release: make(chan struct{})}
}

func (c *blockingCreator) Create(room string, userID int64, content string, createdAt time.Time) error {
	<-c.release

	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++

	return nil
}

func (c *blockingCreator) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.created
}

// The property the whole design rests on: the room event loop calls these, and
// it is single-threaded, so an enqueue that can block would stall every client
// in that room behind one slow HTTP call or database write.
func TestNotifyAndArchiveNeverBlock(t *testing.T) {
	sender := newBlockingSender()
	creator := newBlockingCreator()
	d := NewDispatcher(sender, creator)
	defer func() {
		close(sender.release)
		close(creator.release)
		d.Close(time.Second)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)

		// Well past both queue capacities, with both workers wedged.
		for i := range presenceQueueSize + archiveQueueSize + 100 {
			d.Notify("general", int64(i), domain.PresenceStatusOnline)
			d.Archive("general", int64(i), "hello", time.Now())
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocked: the room event loop would have stalled with it")
	}
}

func TestCloseDrainsQueuedEvents(t *testing.T) {
	sender := newBlockingSender()
	creator := newBlockingCreator()
	d := NewDispatcher(sender, creator)

	const queued = 5
	for i := range queued {
		d.Notify("general", int64(i), domain.PresenceStatusOffline)
		d.Archive("general", int64(i), "hello", time.Now())
	}

	// Let the workers run only once shutdown has already begun.
	close(sender.release)
	close(creator.release)
	d.Close(2 * time.Second)

	if got := sender.count(); got != queued {
		t.Errorf("presence events delivered = %d, want %d", got, queued)
	}
	if got := creator.count(); got != queued {
		t.Errorf("messages archived = %d, want %d", got, queued)
	}
}

// A room goroutine can outlive Close, so a late enqueue must be a dropped event
// and not a panic on a closed channel.
func TestEnqueueAfterCloseDoesNotPanic(t *testing.T) {
	sender := newBlockingSender()
	creator := newBlockingCreator()
	close(sender.release)
	close(creator.release)

	d := NewDispatcher(sender, creator)
	d.Close(time.Second)

	d.Notify("general", 1, domain.PresenceStatusOffline)
	d.Archive("general", 1, "hello", time.Now())
}

func TestCloseIsIdempotent(t *testing.T) {
	sender := newBlockingSender()
	creator := newBlockingCreator()
	close(sender.release)
	close(creator.release)

	d := NewDispatcher(sender, creator)
	d.Close(time.Second)
	d.Close(time.Second)
}
