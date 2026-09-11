// Package dispatch is where a message event becomes webhook calls, off the
// request path. POST /events/message answers 202 the moment the event is
// queued: chat-service's own dispatcher is waiting on that response, and a
// fan-out that has to ask presence-service and then call N receivers with
// retries can take seconds.
package dispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/service"
)

// Consumer-side interfaces so the workers can be driven by stubs in tests.
type RecipientResolver interface {
	Recipients(ctx context.Context, event domain.MessageEvent) ([]service.Recipient, error)
}

type WebhookDeliverer interface {
	Deliver(ctx context.Context, recipient service.Recipient, event domain.MessageEvent)
}

const (
	eventQueueSize    = 256
	deliveryQueueSize = 1024

	fanoutTimeout   = 10 * time.Second
	deliveryTimeout = 30 * time.Second
)

type deliveryJob struct {
	recipient service.Recipient
	event     domain.MessageEvent
}

type Dispatcher struct {
	resolver  RecipientResolver
	deliverer WebhookDeliverer

	eventQ    chan domain.MessageEvent
	deliveryQ chan deliveryJob

	ctx      context.Context
	cancel   context.CancelFunc
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewDispatcher starts one fan-out worker and `workers` delivery workers. The
// fan-out stays single so an event's database reads happen once, in order;
// deliveries are independent of each other and can go as wide as receivers
// tolerate.
func NewDispatcher(resolver RecipientResolver, deliverer WebhookDeliverer, workers int) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		resolver:  resolver,
		deliverer: deliverer,
		eventQ:    make(chan domain.MessageEvent, eventQueueSize),
		deliveryQ: make(chan deliveryJob, deliveryQueueSize),
		ctx:       ctx,
		cancel:    cancel,
		stop:      make(chan struct{}),
	}

	d.wg.Add(1 + workers)
	go d.runFanout()
	for range workers {
		go d.runDelivery()
	}

	return d
}

// Enqueue never blocks. A full queue drops the event and says so: the honest
// failure mode, and the same one chat-service's dispatcher has.
func (d *Dispatcher) Enqueue(event domain.MessageEvent) bool {
	select {
	case d.eventQ <- event:
		return true
	default:
		slog.Error("message event dropped: queue full",
			"service", "notification-service", "event_id", event.EventID, "room", event.Room)
		return false
	}
}

// Close stops accepting work, lets in-flight deliveries finish their current
// attempt (the backoff sleeps are interruptible, so a delivery mid-retry ends
// now rather than seconds later), and waits up to timeout.
func (d *Dispatcher) Close(timeout time.Duration) {
	d.stopOnce.Do(func() {
		close(d.stop)
		d.cancel()
	})

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("dispatcher shutdown timed out; queued deliveries dropped", "service", "notification-service")
	}
}

func (d *Dispatcher) runFanout() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			return
		case event := <-d.eventQ:
			d.fanout(event)
		}
	}
}

func (d *Dispatcher) fanout(event domain.MessageEvent) {
	ctx, cancel := context.WithTimeout(d.ctx, fanoutTimeout)
	defer cancel()

	recipients, err := d.resolver.Recipients(ctx, event)
	if err != nil {
		slog.Error("fan-out failed", "service", "notification-service", "event_id", event.EventID, "error", err)
		return
	}

	for _, recipient := range recipients {
		select {
		case d.deliveryQ <- deliveryJob{recipient: recipient, event: event}:
		default:
			// The delivery row already exists as pending; it stays that way,
			// which is the visible record that this one was dropped.
			slog.Error("delivery dropped: queue full",
				"service", "notification-service", "delivery_id", recipient.Delivery.ID, "user_id", recipient.Webhook.UserID)
		}
	}
}

func (d *Dispatcher) runDelivery() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			return
		case job := <-d.deliveryQ:
			ctx, cancel := context.WithTimeout(d.ctx, deliveryTimeout)
			d.deliverer.Deliver(ctx, job.recipient, job.event)
			cancel()
		}
	}
}
